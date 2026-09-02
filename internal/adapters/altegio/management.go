package altegio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// Cancel deletes an online appointment using the opaque hash returned when it
// was created. The hash is proof for this public operation and never crosses
// the scheduling port except as a private field on Booking.
func (c *Client) Cancel(ctx context.Context, b booking.Booking) error {
	recordID, err := recordID(b.ExternalID)
	if err != nil {
		return err
	}
	if b.ManagementToken == "" {
		return fmt.Errorf("%w: appointment has no management token", booking.ErrRejected)
	}

	_, err = call[json.RawMessage](ctx, c, request{
		method: http.MethodDelete,
		path: "/user/records/" + strconv.FormatInt(recordID, 10) + "/" +
			url.PathEscape(b.ManagementToken),
		repeatable: true, // deleting an already deleted appointment changes nothing
	})
	switch {
	case err == nil, errors.Is(err, booking.ErrNotFound):
		// A retry after a successful response was lost may see 404. Either way,
		// the desired end state has been reached.
		return nil
	case errors.Is(err, booking.ErrUnavailable):
		return fmt.Errorf("altegio cancel: %w: %w", booking.ErrOutcomeUnknown, err)
	default:
		return err
	}
}

// Reschedule moves an online appointment to one exact instant. PUT is safe to
// retry because every attempt requests the same final state; it cannot create
// another appointment.
func (c *Client) Reschedule(
	ctx context.Context,
	b booking.Booking,
	startsAt time.Time,
) (booking.Booking, error) {
	recordID, err := recordID(b.ExternalID)
	if err != nil {
		return booking.Booking{}, err
	}
	if startsAt.IsZero() {
		return booking.Booking{}, fmt.Errorf("%w: new appointment time is empty", booking.ErrRejected)
	}

	_, err = call[json.RawMessage](ctx, c, request{
		method: http.MethodPut,
		path:   "/book_record/" + c.companyID + "/" + strconv.FormatInt(recordID, 10),
		body: rescheduleRequest{
			Datetime: startsAt.In(c.location).Format(time.RFC3339),
			Comment:  "",
		},
		repeatable: true,
	})
	switch {
	case err == nil:
		b.StartsAt = startsAt
		b.Status = booking.StatusConfirmed
		return b, nil
	case errors.Is(err, errRequestRejected):
		return booking.Booking{}, fmt.Errorf("%w: %w", booking.ErrSlotUnavailable, err)
	case errors.Is(err, booking.ErrUnavailable):
		return booking.Booking{}, fmt.Errorf("altegio reschedule: %w: %w", booking.ErrOutcomeUnknown, err)
	default:
		return booking.Booking{}, err
	}
}

func recordID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: appointment reference %q is not an altegio identifier", booking.ErrRejected, raw)
	}
	return id, nil
}
