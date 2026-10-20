package altegio

import (
	"context"
	"net/http"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// AnyStaffEnabled reads the studio's live setting. Missing or unreadable settings
// never silently enable automatic assignment.
func (c *Client) AnyStaffEnabled(ctx context.Context) (bool, error) {
	value, err := call[struct {
		AnyMaster *bool `json:"any_master"`
	}](ctx, c, request{method: http.MethodGet, path: "/locations/" + c.companyID + "/settings/online", repeatable: true})
	if err != nil {
		return false, err
	}
	if value.AnyMaster == nil {
		return false, booking.ErrUnavailable
	}
	return *value.AnyMaster, nil
}
