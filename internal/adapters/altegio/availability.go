package altegio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// dateLayout is how Altegio writes a calendar day.
const dateLayout = "2006-01-02"

// AvailableDates returns the days on which staffID has at least one free slot.
//
// The dates are calendar days in the business's own timezone, not instants, so
// they are returned as midnight in that location rather than converted to UTC.
func (c *Client) AvailableDates(ctx context.Context, staffID string) ([]time.Time, error) {
	query := url.Values{}
	if staffID != "" {
		query.Set("staff_id", staffID)
	}

	path := "/book_dates/" + c.companyID
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	dates, err := call[bookingDates](ctx, c, request{
		method:     http.MethodGet,
		path:       path,
		repeatable: true,
	})
	if err != nil {
		return nil, err
	}

	available := make([]time.Time, 0, len(dates.BookingDates))
	for _, raw := range dates.BookingDates {
		day, err := time.ParseInLocation(dateLayout, raw, c.location)
		if err != nil {
			// One unreadable date must not lose the rest of the calendar.
			c.logger.WarnContext(ctx, "skipped an unreadable altegio date", "value", raw, "error", err)
			continue
		}
		available = append(available, day)
	}
	return available, nil
}

// AvailableSlots returns the free start times for staffID on day.
//
// The result is a snapshot, not a reservation. Nothing is held by reading it,
// so a booking against any of these slots can still be refused, and the
// conversation has to be able to say so.
func (c *Client) AvailableSlots(ctx context.Context, staffID string, day time.Time) ([]booking.Slot, error) {
	if staffID == "" {
		return nil, fmt.Errorf("%w: staff id is required to read availability", booking.ErrRejected)
	}

	dtos, err := call[[]slotDTO](ctx, c, request{
		method:     http.MethodGet,
		path:       "/book_times/" + c.companyID + "/" + staffID + "/" + day.Format(dateLayout),
		repeatable: true,
	})
	if err != nil {
		return nil, err
	}

	slots := make([]booking.Slot, 0, len(dtos))
	for _, dto := range dtos {
		start, err := c.parseSlotStart(dto, day)
		if err != nil {
			c.logger.WarnContext(ctx, "skipped an unreadable altegio slot",
				"datetime", dto.Datetime, "time", dto.Time, "error", err)
			continue
		}

		slots = append(slots, booking.Slot{
			Start:    start,
			Duration: time.Duration(dto.SeanceLength) * time.Second,
			StaffID:  staffID,
		})
	}
	return slots, nil
}

// parseSlotStart resolves a slot's start instant.
//
// The fully qualified datetime is preferred because it carries the location's
// UTC offset. The bare time is a fallback, and combining it with the requested
// day in the business timezone is the only correct way to read it: interpreting
// it anywhere else would move every appointment by the offset.
func (c *Client) parseSlotStart(dto slotDTO, day time.Time) (time.Time, error) {
	if dto.Datetime != "" {
		return time.Parse(time.RFC3339, dto.Datetime)
	}
	if dto.Time == "" {
		return time.Time{}, fmt.Errorf("slot carries neither datetime nor time")
	}
	return time.ParseInLocation(
		dateLayout+" 15:04",
		day.Format(dateLayout)+" "+dto.Time,
		c.location,
	)
}

// parseServiceIDs converts domain service identifiers back to the numeric form
// Altegio expects.
func parseServiceIDs(ids []string) ([]int64, error) {
	parsed := make([]int64, 0, len(ids))
	for _, raw := range ids {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: service id %q is not an altegio identifier", booking.ErrRejected, raw)
		}
		parsed = append(parsed, value)
	}
	return parsed, nil
}
