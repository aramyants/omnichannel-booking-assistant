package altegio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/benefit"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

type certificateTypeDTO struct {
	ID           int64           `json:"id"`
	Title        string          `json:"title"`
	Balance      json.Number     `json:"balance"`
	IsMulti      bool            `json:"is_multi"`
	Archived     bool            `json:"is_archived"`
	Restriction  int             `json:"item_type_id"`
	ExpiryType   int             `json:"expiration_type_id"`
	ExpiryDate   json.RawMessage `json:"expiration_date"`
	ExpiryPeriod int             `json:"expiration_timeout"`
	ExpiryUnit   int             `json:"expiration_timeout_unit_id"`
	OnlineSale   bool            `json:"online_sale_is_enabled"`
}

func certificateAmount(raw json.Number) (float64, error) {
	n, err := raw.Float64()
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, booking.ErrUnavailable
	}
	return n, nil
}

// Altegio's documented examples include numeric codes as well as strings.
// Decode numeric codes without floating-point conversion or loss of digits.
func certificateNumber(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	value := strings.TrimSpace(string(bytes.TrimSpace(raw)))
	if value == "" {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return value
}
func certificateExpiry(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "0" || string(raw) == `""` {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		for _, format := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if _, err := time.Parse(format, text); err == nil {
				return text
			}
		}
		return "invalid"
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil && number > 0 {
		return time.Unix(number, 0).UTC().Format(time.RFC3339)
	}
	return "invalid"
}

func (c *Client) ListCertificateTypes(ctx context.Context) ([]benefit.CertificateType, error) {
	result := []benefit.CertificateType{}
	for page := 1; page <= 5; page++ {
		values, err := call[[]certificateTypeDTO](ctx, c, request{method: http.MethodGet, path: "/locations/" + c.companyID + "/loyalty/certificate_types/search?page=" + strconv.Itoa(page) + "&page_size=100", repeatable: true})
		if err != nil {
			return nil, err
		}
		for _, row := range values {
			if row.Archived {
				continue
			}
			value, err := certificateAmount(row.Balance)
			if err != nil || row.ID <= 0 || strings.TrimSpace(row.Title) == "" {
				return nil, booking.ErrUnavailable
			}
			result = append(result, benefit.CertificateType{ID: strconv.FormatInt(row.ID, 10), Title: row.Title, Value: value, MultipleUse: row.IsMulti, Restriction: row.Restriction, ExpiryType: row.ExpiryType, ExpiryDate: certificateExpiry(row.ExpiryDate), ExpiryPeriod: row.ExpiryPeriod, ExpiryUnit: row.ExpiryUnit, OnlineSaleEnabled: row.OnlineSale})
		}
		if len(values) < 100 {
			return result, nil
		}
	}
	return nil, fmt.Errorf("%w: incomplete certificate catalog", booking.ErrUnavailable)
}

type clientCertificateDTO struct {
	ID      int64           `json:"id"`
	Number  json.RawMessage `json:"number"`
	Balance json.Number     `json:"balance"`
	Expiry  json.RawMessage `json:"expiration_date"`
	Type    struct {
		Title string `json:"title"`
	} `json:"type"`
	Status struct {
		Title string `json:"title"`
	} `json:"status"`
}

// ListPhoneCertificates is privileged. The caller supplies only a phone whose
// ownership was proven by its channel; this is never an assistant model argument.
// The client-scoped summary needs only client-loyalty read access, unlike the
// older /loyalty/certificates route, which also gates on the loyalty module.
func (c *Client) ListPhoneCertificates(ctx context.Context, rawPhone string) ([]benefit.Certificate, error) {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return nil, booking.ErrRejected
	}
	search := clientSearchRequest{Page: 1, PageSize: 100, Fields: []string{"id", "phone"}, Operation: "AND", Filters: []clientSearchFilter{{Type: "quick_search"}}}
	search.Filters[0].State.Value = phone
	clients, err := call[[]clientSearchResult](ctx, c, request{method: http.MethodPost, path: "/company/" + c.companyID + "/clients/search", body: search, repeatable: true})
	if err != nil {
		return nil, err
	}
	if len(clients) >= 100 {
		return nil, booking.ErrUnavailable
	}
	values := []clientCertificateDTO{}
	seenClients := map[int64]bool{}
	for _, client := range clients {
		exact, phoneErr := clientPhone(client.Phone)
		if phoneErr != nil {
			return nil, booking.ErrUnavailable
		}
		if exact != phone || seenClients[client.ID] {
			continue
		}
		if client.ID <= 0 || len(seenClients) >= 10 {
			return nil, booking.ErrUnavailable
		}
		seenClients[client.ID] = true
		summary, readErr := call[struct {
			Certificates *[]clientCertificateDTO `json:"certificates"`
		}](ctx, c, request{method: http.MethodGet, path: "/locations/" + c.companyID + "/client/" + strconv.FormatInt(client.ID, 10) + "/loyalty", repeatable: true})
		if readErr != nil {
			return nil, readErr
		}
		if summary.Certificates == nil || len(values)+len(*summary.Certificates) > 500 {
			return nil, booking.ErrUnavailable
		}
		values = append(values, *summary.Certificates...)
	}
	result := []benefit.Certificate{}
	seenCards := map[string]benefit.Certificate{}
	for _, row := range values {
		balance, err := certificateAmount(row.Balance)
		number := certificateNumber(row.Number)
		if err != nil || row.ID <= 0 || len(number) == 0 || len(number) > 100 {
			return nil, booking.ErrUnavailable
		}
		for _, r := range number {
			if r < 32 || r == '<' || r == '>' {
				return nil, booking.ErrUnavailable
			}
		}
		tail := []rune(number)
		if len(tail) > 4 {
			tail = tail[len(tail)-4:]
		}
		card := benefit.Certificate{ID: strconv.FormatInt(row.ID, 10), Title: row.Type.Title, Balance: balance, Number: number, MaskedNumber: "•••• " + string(tail), ExpiresAt: certificateExpiry(row.Expiry), Status: row.Status.Title}
		if previous, exists := seenCards[card.ID]; exists {
			if previous != card {
				return nil, booking.ErrUnavailable
			}
			continue
		}
		seenCards[card.ID] = card
		result = append(result, card)
	}
	return result, nil
}
