package altegio

import (
	"context"
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ListPhoneBookings is privileged. Its phone comes only from completed cabinet
// ownership proof; the model cannot supply a lookup phone or client ID.
func (c *Client) ListPhoneBookings(ctx context.Context, rawPhone string, now time.Time) ([]booking.Booking, error) {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return nil, booking.ErrRejected
	}
	search := clientSearchRequest{Page: 1, PageSize: 100, Fields: []string{"id", "name", "phone"}, Operation: "AND", Filters: []clientSearchFilter{{Type: "quick_search"}}}
	search.Filters[0].State.Value = phone
	clients, err := call[[]clientSearchResult](ctx, c, request{method: http.MethodPost, path: "/company/" + c.companyID + "/clients/search", body: search, repeatable: true})
	if err != nil {
		return nil, err
	}
	if len(clients) >= 100 {
		return nil, fmt.Errorf("%w: incomplete client search", booking.ErrUnavailable)
	}
	booked := []booking.Booking{}
	seen := map[string]bool{}
	for _, client := range clients {
		exact, err := clientPhone(client.Phone)
		if err != nil {
			return nil, fmt.Errorf("%w: client contact unavailable", booking.ErrUnavailable)
		}
		if exact != phone {
			continue
		}
		if client.ID <= 0 {
			return nil, booking.ErrUnavailable
		}
		for page := 1; page <= 20; page++ {
			query := url.Values{"client_id": {strconv.FormatInt(client.ID, 10)}, "start_date": {now.In(c.location).Format("2006-01-02")}, "page": {strconv.Itoa(page)}, "count": {"100"}, "with_deleted": {"1"}}
			records, err := call[[]nativeRecord](ctx, c, request{method: http.MethodGet, path: "/records/" + c.companyID + "?" + query.Encode(), repeatable: true})
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				if record.ID <= 0 || strconv.FormatInt(record.CompanyID, 10) != c.companyID {
					return nil, booking.ErrUnavailable
				}
				if record.Client == nil || record.Client.ID != client.ID {
					return nil, fmt.Errorf("%w: appointment owner mismatch", booking.ErrUnavailable)
				}
				returned, err := normalizeAltegioPhone(record.Client.Phone)
				if err != nil || returned != phone {
					return nil, fmt.Errorf("%w: appointment phone mismatch", booking.ErrUnavailable)
				}
				ref := strconv.FormatInt(record.ID, 10)
				if seen[ref] {
					continue
				}
				snap, err := c.nativeSnapshot(record, record.ID)
				if err != nil {
					return nil, err
				}
				seen[ref] = true
				booked = append(booked, snap.Booking)
			}
			if len(records) < 100 {
				break
			}
			if page == 20 {
				return nil, fmt.Errorf("%w: appointment history exceeds pagination bound", booking.ErrUnavailable)
			}
		}
	}
	return booked, nil
}
