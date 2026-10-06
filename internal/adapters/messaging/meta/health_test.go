package meta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestMessagingHealthIgnoresCallingBlocksAndPendingDisplayName(t *testing.T) {
	for _, test := range []struct {
		status, quality string
		blocked, paused bool
	}{{"LIMITED", "GREEN", false, false}, {"BLOCKED", "GREEN", true, false}, {"AVAILABLE", "RED", false, true}} {
		t.Run(test.status+test.quality, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/message_templates") {
					_, _ = w.Write([]byte(`{"data":[{"name":"booking","language":"en","status":"PAUSED","category":"UTILITY"},{"name":"booking_ru","language":"ru","status":"APPROVED","category":"UTILITY"}]}`))
					return
				}
				_, _ = fmt.Fprintf(w, `{"quality_rating":%q,"health_status":{"can_send_message":%q,"entities":[{"entity_type":"PHONE_NUMBER","can_send_message":%q,"can_receive_call_sip":"BLOCKED"},{"entity_type":"WABA","id":"waba","can_send_message":"AVAILABLE"}]}}`, test.quality, test.status, test.status)
			}))
			defer server.Close()
			client, _ := NewClient("token", "phone", WithBaseURL(server.URL))
			health, err := client.ReadWhatsAppHealth(t.Context(), map[string]bool{"booking:en": true, "booking_ru:ru": true})
			if err != nil || !health.Known || health.Blocked != test.blocked || health.ProactivePaused != test.paused {
				t.Fatalf("health=%+v err=%v", health, err)
			}
			if !test.blocked && (health.Templates["booking:en"] || !health.Templates["booking_ru:ru"]) {
				t.Fatalf("template state=%+v", health)
			}
		})
	}
}

func TestDeliveryGuardDoesNotPostBlockedMessages(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts++; _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	client, _ := NewClient("token", "phone", WithBaseURL(server.URL))
	client.SetDeliveryGuard(func(_ context.Context, name, lang string) (bool, error) {
		return name != "booking" || lang != "en", nil
	})
	err := client.SendBookingTemplate(t.Context(), "client", "booking", "en", []string{"confirmed", "tomorrow", "massage"})
	if !errors.Is(err, ErrRejected) || !errors.Is(err, messaging.ErrDeliveryRejected) || posts != 0 {
		t.Fatalf("unsafe guard result posts=%d err=%v", posts, err)
	}
	if err := client.Send(t.Context(), messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "client", Text: "Hello"}); err != nil || posts != 1 {
		t.Fatalf("reply blocked posts=%d err=%v", posts, err)
	}
}

func TestTemplateMetadataFailureDoesNotFabricateRestriction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/message_templates") {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"quality_rating":"GREEN","health_status":{"can_send_message":"AVAILABLE","entities":[{"entity_type":"WABA","id":"waba","can_send_message":"AVAILABLE"}]}}`))
	}))
	defer server.Close()
	client, _ := NewClient("token", "phone", WithBaseURL(server.URL))
	health, err := client.ReadWhatsAppHealth(t.Context(), map[string]bool{"booking:en": true})
	if err != nil || !health.Known || health.Blocked || health.TemplatesKnown {
		t.Fatalf("partial health=%+v err=%v", health, err)
	}
}
