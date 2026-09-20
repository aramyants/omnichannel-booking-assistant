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

// ReadBooking refreshes only the already-owned appointment identified by its
// private online-booking hash. Reminders must not use stale local snapshots
// after a receptionist edits or cancels the visit directly in Altegio.
func (c *Client) ReadBooking(ctx context.Context, b booking.Booking) (booking.Booking, error) {
	record, err := recordID(b.ExternalID)
	if err != nil {
		return b, err
	}
	if b.ManagementToken == "" {
		return b, fmt.Errorf("%w: no online booking proof", booking.ErrRejected)
	}
	type details struct {
		ID       int64  `json:"id"`
		Datetime string `json:"datetime"`
		Length   int64  `json:"length"`
		Deleted  bool   `json:"deleted"`
		Staff    struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"staff"`
		Services []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"services"`
	}
	dto, err := call[details](ctx, c, request{method: http.MethodGet, path: "/book_record/" + c.companyID + "/" + strconv.FormatInt(record, 10) + "/" + url.PathEscape(b.ManagementToken), repeatable: true})
	if err != nil {
		return b, err
	}
	if dto.ID != record {
		return b, fmt.Errorf("%w: appointment identity did not match", booking.ErrUnavailable)
	}
	if dto.Deleted {
		b.Status = booking.StatusCancelled
		return b, nil
	}
	var starts time.Time
	for _, format := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
		if parsed, e := time.Parse(format, dto.Datetime); e == nil {
			starts = parsed
			break
		}
	}
	if starts.IsZero() || dto.Length <= 0 || dto.Staff.ID <= 0 || len(dto.Services) == 0 {
		return b, fmt.Errorf("%w: incomplete online booking details", booking.ErrUnavailable)
	}
	b.StartsAt, b.Duration, b.StaffID, b.StaffName = starts, time.Duration(dto.Length)*time.Second, strconv.FormatInt(dto.Staff.ID, 10), dto.Staff.Name
	oldNames := map[string]string{}
	for i, id := range b.ServiceIDs {
		if i < len(b.ServiceNames) {
			oldNames[id] = b.ServiceNames[i]
		}
	}
	b.ServiceIDs, b.ServiceNames = nil, nil
	for _, service := range dto.Services {
		id := strconv.FormatInt(service.ID, 10)
		name := oldNames[id]
		if name == "" {
			name = service.Title
		}
		b.ServiceIDs = append(b.ServiceIDs, id)
		b.ServiceNames = append(b.ServiceNames, name)
	}
	return b, nil
}
