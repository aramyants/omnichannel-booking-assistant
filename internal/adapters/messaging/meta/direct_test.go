package meta

import (
	"encoding/json"
	"errors"
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
