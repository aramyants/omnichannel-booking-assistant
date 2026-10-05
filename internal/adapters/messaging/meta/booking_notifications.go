package meta

import (
	"context"
	"strings"
)

// SendBookingTemplate requires an approved utility template with three body
// parameters: booking event, local date/time, services and specialist.
func (c *Client) SendBookingTemplate(ctx context.Context, to, name, lang string, values []string) error {
	var params []any
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			value = "—"
		}
		params = append(params, map[string]string{"type": "text", "text": value})
	}
	return c.post(ctx, c.phoneNumberID+"/messages", map[string]any{"messaging_product": "whatsapp", "to": to, "type": "template", "template": map[string]any{"name": name, "language": map[string]string{"code": lang}, "components": []any{map[string]any{"type": "body", "parameters": params}}}})
}
