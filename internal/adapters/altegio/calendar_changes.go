package altegio

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ListChangedNativeBookings discovers IDs only. Every queued notification
// independently re-reads the privileged record before selecting a recipient.
func (c *Client) ListChangedNativeBookings(ctx context.Context, from, to time.Time) ([]string, error) {
	if !to.After(from) || to.Sub(from) > 25*time.Hour {
		return nil, booking.ErrRejected
	}
	var ids []string
	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		q := url.Values{"changed_after": {from.In(c.location).Format("2006-01-02T15:04:05")}, "changed_before": {to.In(c.location).Format("2006-01-02T15:04:05")}, "include_deleted": {"true"}, "page": {strconv.Itoa(page)}, "limit": {"100"}}
		rows, err := call[[]struct {
			ID json.RawMessage `json:"id"`
		}](ctx, c, request{method: http.MethodGet, path: "/locations/" + c.companyID + "/appointments?" + q.Encode(), repeatable: true})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var id string
			if err = json.Unmarshal(row.ID, &id); err != nil {
				var n json.Number
				if err = json.Unmarshal(row.ID, &n); err != nil {
					return nil, booking.ErrUnavailable
				}
				id = n.String()
			}
			value, parseErr := strconv.ParseUint(id, 10, 64)
			if parseErr != nil || value == 0 || seen[id] {
				return nil, fmt.Errorf("%w: invalid or repeated calendar page", booking.ErrUnavailable)
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(rows) < 100 {
			return ids, nil
		}
	}
	return nil, fmt.Errorf("%w: calendar change window exceeds pagination bound", booking.ErrUnavailable)
}
