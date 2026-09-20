package meta

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// TemplateReminders never falls back to free-form out-of-window messages.
type TemplateReminders struct {
	*Client
	Templates map[string]string
	Location  *time.Location
}

func (s TemplateReminders) SupportsReminder(lang string) bool { return s.Templates[lang] != "" }
func (s TemplateReminders) SendReminder(ctx context.Context, msg messaging.Outgoing, lang string, b booking.Booking, calendarURL string) error {
	name := s.Templates[lang]
	if name == "" || msg.Provider != messaging.ProviderWhatsApp || calendarURL == "" {
		return errors.New("approved reminder template or private calendar link unavailable")
	}
	location := s.Location
	if location == nil {
		location = time.UTC
	}
	local := b.StartsAt.In(location)
	var params []any
	for _, value := range []string{b.CustomerName, local.Format("02.01.2006"), local.Format("15:04"), strings.Join(b.ServiceNames, ", "), b.StaffName, calendarURL} {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			value = "—"
		}
		params = append(params, map[string]string{"type": "text", "text": value})
	}
	payload := map[string]any{"messaging_product": "whatsapp", "to": msg.ExternalThreadID, "type": "template", "template": map[string]any{"name": name, "language": map[string]string{"code": lang}, "components": []any{map[string]any{"type": "body", "parameters": params}}}}
	return s.post(ctx, s.phoneNumberID+"/messages", payload)
}
