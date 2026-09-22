package altegio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

// appointmentSlotID numbers the appointment inside a request. Altegio requires
// the field even when only one appointment is being made, and this system never
// books more than one at a time.
const appointmentSlotID = 1

// Check asks Altegio whether a booking would be accepted, without creating it.
//
// It is the difference between telling a customer their time is free and
// finding out at the moment of booking that it is not. A refusal here is
// reported as ErrSlotUnavailable: the request was well formed, so the reason it
// was rejected is that the slot has gone.
func (c *Client) Check(ctx context.Context, selection booking.Selection) error {
	appointment, err := c.toAppointment(selection)
	if err != nil {
		return err
	}

	_, err = call[json.RawMessage](ctx, c, request{
		method:     http.MethodPost,
		path:       "/book_check/" + c.companyID,
		body:       bookCheckRequest{Appointments: []appointmentRequest{appointment}},
		repeatable: true, // validation changes nothing, so it is safe to repeat
	})
	if err == nil {
		return nil
	}

	// A rejection of a well-formed request means the slot is no longer free.
	// A refusal naming fields means this system built the request wrongly, and
	// is deliberately not translated: it keeps ErrRejected and reaches the
	// customer as a failure rather than as a time somebody else has taken.
	// Transport failures keep their own meaning so the caller can retry them.
	if errors.Is(err, errRequestRejected) {
		return fmt.Errorf("%w: %w", booking.ErrSlotUnavailable, err)
	}
	return err
}

// Create books the appointment and returns it only once Altegio has confirmed.
//
// The request is never retried. Altegio is told this system's idempotency key,
// but nothing in the published API guarantees that a repeat is recognised, and
// the cost of being wrong is a customer with two appointments. A request whose
// outcome is not learned is reported as ErrOutcomeUnknown so the caller
// reconciles rather than guessing.
func (c *Client) Create(ctx context.Context, req booking.Request) (booking.Booking, error) {
	phone, err := customer.NormalizePhone(req.Phone)
	if err != nil {
		return booking.Booking{}, fmt.Errorf("%w: %w", booking.ErrRejected, err)
	}
	appointment, err := c.toAppointment(req.Selection())
	if err != nil {
		return booking.Booking{}, err
	}

	records, err := call[[]recordDTO](ctx, c, request{
		method: http.MethodPost,
		path:   "/book_record/" + c.companyID,
		body: bookRecordRequest{
			Phone:         phone,
			FullName:      req.CustomerName,
			Email:         req.Email,
			Comment:       req.Comment,
			Appointments:  []appointmentRequest{appointment},
			APIID:         req.IdempotencyKey,
			NotifyBySMS:   0,
			NotifyByEmail: 0,
		},
		repeatable: false,
	})
	if err != nil {
		if errors.Is(err, errRequestRejected) {
			// The request passed validation on its way in, so a refusal here
			// almost always means somebody took the slot in between.
			return booking.Booking{}, fmt.Errorf("%w: %w", booking.ErrSlotUnavailable, err)
		}
		return booking.Booking{}, err
	}

	if len(records) == 0 {
		// Altegio accepted the call but named no appointment. Whether one was
		// created cannot be told from here, and guessing either way risks
		// telling a customer something untrue.
		return booking.Booking{}, fmt.Errorf(
			"altegio book_record: %w: accepted the request but returned no appointment",
			booking.ErrOutcomeUnknown,
		)
	}

	record := records[0]
	created := booking.Booking{
		ID:              id.New(),
		ExternalID:      strconv.FormatInt(record.RecordID, 10),
		ManagementToken: record.RecordHash,
		CustomerID:      req.CustomerID,
		ServiceIDs:      req.ServiceIDs,
		StaffID:         req.StaffID,
		StartsAt:        req.StartsAt,
		Duration:        req.Duration,
		Status:          booking.StatusConfirmed,
		CreatedAt:       time.Now().UTC(),
	}

	// The public booking endpoint attaches an existing client by phone but does
	// not replace that client's old profile name. Historical API diagnostics can
	// therefore leak into the front desk even though the customer supplied their
	// real name. Repair only that unmistakable placeholder, after confirmation,
	// and never let this optional cleanup turn a real booking into a failure.
	if c.repairClientNames && c.userToken != "" && strings.TrimSpace(req.CustomerName) != "" {
		cleanupCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if err := c.repairDiagnosticClientName(cleanupCtx, phone, req.CustomerName); err != nil {
			c.logger.WarnContext(ctx, "could not repair an altegio diagnostic client name", "error", err)
		}
		cancel()
	}
	return created, nil
}

type clientSearchRequest struct {
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
	Fields    []string             `json:"fields"`
	Operation string               `json:"operation"`
	Filters   []clientSearchFilter `json:"filters"`
}

type clientSearchFilter struct {
	Type  string `json:"type"`
	State struct {
		Value string `json:"value"`
	} `json:"state"`
}

type clientSearchResult struct {
	ID    int64           `json:"id"`
	Name  string          `json:"name"`
	Phone json.RawMessage `json:"phone"`
}

func (c *Client) repairDiagnosticClientName(ctx context.Context, phone, desiredName string) error {
	search := clientSearchRequest{
		Page: 1, PageSize: 10,
		Fields: []string{"id", "name", "phone"}, Operation: "AND",
		Filters: []clientSearchFilter{{Type: "quick_search"}},
	}
	search.Filters[0].State.Value = phone
	clients, err := call[[]clientSearchResult](ctx, c, request{
		method: http.MethodPost, path: "/company/" + c.companyID + "/clients/search",
		body: search, repeatable: true,
	})
	if err != nil {
		return fmt.Errorf("search client: %w", err)
	}

	var match *clientSearchResult
	for i := range clients {
		candidate := &clients[i]
		candidatePhone, err := clientPhone(candidate.Phone)
		if err != nil || candidatePhone != phone || !isDiagnosticClientName(candidate.Name) {
			continue
		}
		if match != nil {
			return errors.New("more than one diagnostic client matched the booking phone")
		}
		match = candidate
	}
	if match == nil {
		return nil
	}

	_, err = call[json.RawMessage](ctx, c, request{
		method: http.MethodPut,
		path:   "/client/" + c.companyID + "/" + strconv.FormatInt(match.ID, 10),
		body: struct {
			Name  string `json:"name"`
			Phone string `json:"phone"`
		}{Name: strings.TrimSpace(desiredName), Phone: phone},
		repeatable: true,
	})
	if err != nil {
		return fmt.Errorf("update client: %w", err)
	}
	return nil
}

func isDiagnosticClientName(name string) bool {
	return strings.Contains(strings.ToLower(strings.Join(strings.Fields(name), " ")), "api diagnostic")
}

func clientPhone(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if len(value) > 1 && value[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
	}
	return customer.NormalizePhone(value)
}

// toAppointment converts a domain request into Altegio's shape.
func (c *Client) toAppointment(selection booking.Selection) (appointmentRequest, error) {
	services, err := parseServiceIDs(selection.ServiceIDs)
	if err != nil {
		return appointmentRequest{}, err
	}

	staffID, err := strconv.ParseInt(selection.StaffID, 10, 64)
	if err != nil {
		return appointmentRequest{}, fmt.Errorf(
			"%w: staff id %q is not an altegio identifier", booking.ErrRejected, selection.StaffID)
	}

	return appointmentRequest{
		ID:       appointmentSlotID,
		Services: services,
		StaffID:  staffID,
		// Sent in the business's own timezone with an explicit offset, so the
		// appointment lands at the hour the customer asked for rather than the
		// same instant read somewhere else.
		Datetime: selection.StartsAt.In(c.location).Format(time.RFC3339),
	}, nil
}
