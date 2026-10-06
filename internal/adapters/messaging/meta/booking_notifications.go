package meta

import (
	"context"
	"strings"
)

// SendBookingTemplate requires an approved utility template with three body
// parameters: booking event, local date/time, services and specialist.
func (c *Client) SendBookingTemplate(ctx context.Context, to, name, lang string, values []string) error {
	return c.SendBookingTemplateWithReference(ctx, to, name, lang, values, "")
}

// SendBookingTemplateWithReference attaches an opaque native-event reference
// returned in Meta's signed delivery status webhook. It contains no client data.
func (c *Client) SendBookingTemplateWithReference(ctx context.Context, to, name, lang string, values []string, reference string) error {
	var params []any
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			value = "—"
		}
		params = append(params, map[string]string{"type": "text", "text": value})
	}
	payload := map[string]any{"messaging_product": "whatsapp", "to": to, "type": "template", "template": map[string]any{"name": name, "language": map[string]string{"code": lang}, "components": []any{map[string]any{"type": "body", "parameters": params}}}}
	if reference != "" {
		payload["biz_opaque_callback_data"] = reference
	}
	return c.post(ctx, c.phoneNumberID+"/messages", payload)
}
