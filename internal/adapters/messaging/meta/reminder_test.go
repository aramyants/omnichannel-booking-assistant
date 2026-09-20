package meta

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestReminderUsesApprovedTemplateAndNeverFallsBack(t *testing.T) {
	var payloads []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"messages":[{"id":"sent"}]}`))
	}))
	defer srv.Close()
	client, err := NewClient("test-token", "123", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	sender := TemplateReminders{Client: client, Templates: map[string]string{"ru": "appointment_reminder_ru"}, Location: time.FixedZone("Asia/Yerevan", 4*60*60)}
	b := booking.Booking{CustomerName: "Test\nCustomer", StartsAt: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), ServiceNames: []string{"Motion Relax"}, StaffName: "Therapist"}
	msg := messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "15550000000", Text: "must not be sent as free form"}
	link := "https://example.test/calendar/123?key=private"
	if err := sender.SendReminder(t.Context(), msg, "ru", b, link); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 || payloads[0]["type"] != "template" {
		t.Fatal("not a template")
	}
	template := payloads[0]["template"].(map[string]any)
	if template["name"] != "appointment_reminder_ru" {
		t.Fatal("wrong approved template")
	}
	params := template["components"].([]any)[0].(map[string]any)["parameters"].([]any)
	for i, want := range []string{"Test Customer", "25.09.2026", "15:00", "Motion Relax", "Therapist", link} {
		if params[i].(map[string]any)["text"] != want {
			t.Fatalf("parameter %d incorrect", i+1)
		}
	}
	if sender.SupportsReminder("en") {
		t.Fatal("unconfigured template supported")
	}
	if err := sender.SendReminder(t.Context(), msg, "en", b, link); err == nil {
		t.Fatal("missing template accepted")
	}
	if err := sender.SendReminder(t.Context(), msg, "ru", b, ""); err == nil {
		t.Fatal("missing calendar link accepted")
	}
	if len(payloads) != 1 {
		t.Fatal("unsafe free-form fallback sent")
	}
}
