package altegio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
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
	appointment, err := c.toAppointment(req.Selection())
	if err != nil {
		return booking.Booking{}, err
	}

	records, err := call[[]recordDTO](ctx, c, request{
		method: http.MethodPost,
		path:   "/book_record/" + c.companyID,
		body: bookRecordRequest{
			Phone:         req.Phone,
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
	return booking.Booking{
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
	}, nil
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
