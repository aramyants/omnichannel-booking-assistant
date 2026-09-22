package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func directUpdate(object, entryID, event string) []byte {
	return []byte(`{"object":"` + object + `","entry":[{"id":"` + entryID + `","messaging":[` + event + `]}]}`)
}

const directEvent = `{"sender":{"id":"customer-1"},"recipient":{"id":"business-1"},"timestamp":1788868800000,"message":{"mid":"message-1","text":"Можно на русском?"}}`

func TestDirectChannelsVerifyAndRouteCustomerMessages(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger} {
		t.Run(string(provider), func(t *testing.T) {
			object := "page"
			if provider == messaging.ProviderInstagram {
				object = "instagram"
			}
			body := directUpdate(object, "business-1", directEvent)
			messages := &recordingHandler{}
			h := newDirectHandler(testWebhook(t), messages, discardLogger(), provider, "business-1")
			if rec := post(t, h, "invalid", body); rec.Code != http.StatusUnauthorized || len(messages.got) != 0 {
				t.Fatal("unsigned message reached the assistant")
			}
			if rec := post(t, h, sign(body), body); rec.Code != http.StatusOK || len(messages.got) != 1 {
				t.Fatalf("signed delivery failed: %d, %+v", rec.Code, messages.got)
			}
			msg := messages.got[0]
			if msg.Provider != provider || msg.ExternalUserID != "customer-1" || msg.ExternalThreadID != "customer-1" || msg.ExternalMessageID != "message-1" || msg.Content.Text != "Можно на русском?" {
				t.Fatalf("bad normalization: %+v", msg)
			}
			if msg.SentAt.UnixMilli() != 1788868800000 {
				t.Fatal("timestamp is not milliseconds")
			}
			messages.err = errors.New("temporarily unavailable")
			if rec := post(t, h, sign(body), body); rec.Code != http.StatusInternalServerError {
				t.Fatal("failed delivery was acknowledged")
			}
		})
	}
}

func TestDirectChannelsIgnoreEchoesReceiptsAndOtherAccounts(t *testing.T) {
	for name, body := range map[string][]byte{
		"other account":   directUpdate("instagram", "other", directEvent),
		"wrong recipient": directUpdate("instagram", "business-1", strings.Replace(directEvent, `"recipient":{"id":"business-1"}`, `"recipient":{"id":"other"}`, 1)),
		"echo":            directUpdate("instagram", "business-1", strings.Replace(directEvent, `"mid":`, `"is_echo":true,"mid":`, 1)),
		"deleted":         directUpdate("instagram", "business-1", strings.Replace(directEvent, `"mid":`, `"is_deleted":true,"mid":`, 1)),
		"receipt":         directUpdate("instagram", "business-1", `{"read":{"watermark":123}}`),
		"other channel":   directUpdate("page", "business-1", directEvent),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseDirect(body, receivedAt, messaging.ProviderInstagram, "business-1")
			if err != nil || len(got) != 0 {
				t.Fatalf("unexpected message: %+v, %v", got, err)
			}
		})
	}
}

func TestDirectReactionBecomesNonConfirmingConversationContext(t *testing.T) {
	event := `{"sender":{"id":"customer-1"},"recipient":{"id":"business-1"},"timestamp":1788868800000,"reaction":{"mid":"message-previous","action":"react","reaction":"love","emoji":"❤"}}`
	got, err := parseDirect(directUpdate("instagram", "business-1", event), receivedAt, messaging.ProviderInstagram, "business-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("reaction = %+v, err = %v", got, err)
	}
	if !strings.Contains(got[0].Content.Text, "feedback only") || !strings.Contains(got[0].Content.Text, "not confirmation") {
		t.Fatalf("unsafe reaction context: %q", got[0].Content.Text)
	}
}

func TestDirectChannelsSendCorrectPayloadAndHost(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger} {
		t.Run(string(provider), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v22.0/business-1/messages" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("bad request: %s %s", r.Method, r.URL.Path)
				}
				var payload directTextRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.Recipient.ID != "customer-1" || payload.Message.Text != "Hello" {
					t.Errorf("bad body: %+v", payload)
				}
				wantType := ""
				if provider == messaging.ProviderMessenger {
					wantType = "RESPONSE"
				}
				if payload.MessagingType != wantType {
					t.Errorf("messaging_type = %q", payload.MessagingType)
				}
				_, _ = w.Write([]byte(`{"message_id":"sent-1"}`))
			}))
			defer srv.Close()
			client, err := newDirectClient("test-token", "business-1", provider, WithBaseURL(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Send(t.Context(), messaging.Outgoing{Provider: provider, ExternalThreadID: "customer-1", Text: "Hello"}); err != nil {
				t.Fatal(err)
			}
			if err := client.Send(t.Context(), messaging.Outgoing{Provider: messaging.ProviderTelegram, ExternalThreadID: "customer-1", Text: "Hello"}); err == nil {
				t.Fatal("accepted wrong channel")
			}
			production, err := newDirectClient("test-token", "business-1", provider)
			if err != nil {
				t.Fatal(err)
			}
			wantHost := "https://graph.facebook.com"
			if provider == messaging.ProviderInstagram {
				wantHost = "https://graph.instagram.com"
			}
			if production.client.baseURL != wantHost {
				t.Fatalf("host = %s", production.client.baseURL)
			}
		})
	}
}

func TestDirectConfirmationUsesNativeWebButtonsAndTypingFeedback(t *testing.T) {
	var payloads []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"message_id":"sent"}`))
	}))
	defer srv.Close()

	client, err := newDirectClient("token", "business-1", messaging.ProviderInstagram, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	msg := messaging.Outgoing{Provider: messaging.ProviderInstagram, ExternalThreadID: "customer-1", Text: "Your appointment is confirmed."}.WithLinks([]messaging.Link{
		{Label: "Add to calendar", URL: "https://example.com/calendar"},
		{Label: "Yandex Maps", URL: "https://example.com/yandex"},
		{Label: "Google Maps", URL: "https://example.com/google"},
		{Label: "Instagram", URL: "https://example.com/instagram"},
	})
	if err := client.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	envelope := messaging.Envelope{ExternalThreadID: "customer-1"}
	if err := client.BeginFeedback(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if err := client.EndFeedback(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}

	if len(payloads) != 3 {
		t.Fatalf("payload count = %d, want confirmation + typing on/off", len(payloads))
	}
	message := payloads[0]["message"].(map[string]any)
	attachment := message["attachment"].(map[string]any)
	template := attachment["payload"].(map[string]any)
	buttons := template["buttons"].([]any)
	if template["template_type"] != "button" || len(buttons) != 3 {
		t.Fatalf("button template = %+v", template)
	}
	if buttons[0].(map[string]any)["url"] != "https://example.com/calendar" || buttons[1].(map[string]any)["url"] != "https://example.com/yandex" {
		t.Fatalf("priority actions = %+v", buttons)
	}
	if payloads[1]["sender_action"] != "typing_on" || payloads[2]["sender_action"] != "typing_off" {
		t.Fatalf("typing actions = %+v", payloads[1:])
	}
}

func TestDirectChoicesUsePersistentPostbackButtonsWithoutRepeatingThePrompt(t *testing.T) {
	var payloads []directButtonRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload directButtonRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"message_id":"sent"}`))
	}))
	defer srv.Close()

	client, err := newDirectClient("token", "business-1", messaging.ProviderInstagram, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	msg := messaging.Outgoing{
		Provider: messaging.ProviderInstagram, ExternalThreadID: "customer-1",
		ChoiceToken: "choice-token", Text: "Which specialist works for you?",
		Choices: []messaging.Choice{{Label: "Galina"}, {Label: "Yaroslava"}, {Label: "Elvira"}, {Label: "Garik"}},
	}
	if err := client.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want two button templates", len(payloads))
	}
	first := payloads[0].Message.Attachment.Payload
	second := payloads[1].Message.Attachment.Payload
	if first.Text != msg.Text || second.Text != "\u21b3" {
		t.Fatalf("template text = %q, %q", first.Text, second.Text)
	}
	if len(first.Buttons) != 3 || len(second.Buttons) != 1 {
		t.Fatalf("button groups = %d, %d", len(first.Buttons), len(second.Buttons))
	}
	for _, button := range append(first.Buttons, second.Buttons...) {
		if button.Type != "postback" || button.URL != "" {
			t.Fatalf("button = %+v", button)
		}
		token, label := decodeChoice(button.Payload)
		if token != msg.ChoiceToken || label == "" {
			t.Fatalf("invalid button payload: %+v", button)
		}
	}
}

func TestDirectPostbackBecomesAValidatedChoice(t *testing.T) {
	payload := choicePayload("choice-token", "Garik")
	event := fmt.Sprintf(`{"sender":{"id":"customer-1"},"recipient":{"id":"business-1"},"timestamp":1788868800000,"postback":{"mid":"postback-1","title":"Garik","payload":%q}}`, payload)
	got, err := parseDirect(directUpdate("instagram", "business-1", event), receivedAt, messaging.ProviderInstagram, "business-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("postback = %+v, err = %v", got, err)
	}
	if got[0].Content.Text != "Garik" || got[0].ChoiceMessageID != "choice-token" || got[0].ExternalMessageID != "postback-1" {
		t.Fatalf("postback normalization = %+v", got[0])
	}
}

func TestDirectProfileLookupSuppliesKnownNameAndCachesIt(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v22.0/customer-1" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(r.URL.Query().Get("fields"), "first_name") {
			t.Fatalf("fields = %q", r.URL.Query().Get("fields"))
		}
		_, _ = w.Write([]byte(`{"first_name":"Anna","last_name":"Petrosyan","locale":"hy_AM"}`))
	}))
	defer srv.Close()
	client, err := newDirectClient("token", "business-1", messaging.ProviderMessenger, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	envelope := messaging.Envelope{ExternalUserID: "customer-1"}
	for i := 0; i < 2; i++ {
		profile, err := client.ResolveProfile(t.Context(), envelope)
		if err != nil || profile.DisplayName != "Anna Petrosyan" || profile.Language != "hy-AM" {
			t.Fatalf("profile = %+v, err = %v", profile, err)
		}
	}
	if requests != 1 {
		t.Fatalf("profile requested %d times, want cached after the first", requests)
	}
}

func TestDirectAttachmentsBecomeReadableFallbackAndMissingIDsAreRejected(t *testing.T) {
	body := directUpdate("instagram", "business-1", strings.Replace(directEvent, `"text":"Можно на русском?"`, `"attachments":[{"type":"audio"}]`, 1))
	got, err := parseDirect(body, receivedAt, messaging.ProviderInstagram, "business-1")
	if err != nil || len(got) != 1 || got[0].Content.Type != messaging.ContentTypeUnsupported {
		t.Fatalf("attachment lost: %v %v", got, err)
	}
	body = directUpdate("instagram", "business-1", strings.Replace(directEvent, `"mid":"message-1",`, "", 1))
	if _, err := parseDirect(body, receivedAt, messaging.ProviderInstagram, "business-1"); err == nil {
		t.Fatal("accepted missing dedupe identifier")
	}
}
