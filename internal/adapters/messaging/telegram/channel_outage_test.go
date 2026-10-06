package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestWhatsAppStaffFollowupUsesNativeChannelAndNoTelegramCustomerControls(t *testing.T) {
	var text string
	var markup any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		text, _ = body["text"].(string)
		markup = body["reply_markup"]
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()
	notifier, err := NewStaffNotifier(NewClient("token", WithBaseURL(server.URL)), "staff", nil)
	if err != nil {
		t.Fatal(err)
	}
	notice := assistant.HandoffNotice{Provider: messaging.ProviderWhatsApp, ExternalUserID: "37495152507", Reason: assistant.ReasonChannelUnavailable}
	if err := notifier.NotifyInbound(t.Context(), notice, "Please call me"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Please call me") || !strings.Contains(text, "https://wa.me/37495152507") || markup != nil {
		t.Fatalf("text=%s markup=%v", text, markup)
	}
	if title := formatHandoff(notice); !strings.Contains(title, "CHANNEL UNAVAILABLE") || strings.Contains(title, "UNRESOLVED BOOKING") {
		t.Fatalf("misleading notice: %s", title)
	}
}
