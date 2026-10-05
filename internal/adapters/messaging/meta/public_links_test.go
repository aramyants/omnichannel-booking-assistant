package meta

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestSocialDestinationsAndChoicesAreBothDelivered(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger, messaging.ProviderWhatsApp} {
		t.Run(string(provider), func(t *testing.T) {
			var payloads []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				payloads = append(payloads, body)
				_, _ = w.Write([]byte(`{"message_id":"sent","messages":[{"id":"sent"}]}`))
			}))
			defer server.Close()
			msg := messaging.Outgoing{Provider: provider, ExternalThreadID: "client", Text: "Contact & socials", ChoiceToken: "test", Choices: []messaging.Choice{{Label: "Book a visit"}, {Label: "Services"}, {Label: "My appointments"}, {Label: "Talk to a person"}}}
			for i := 0; i < 10; i++ {
				msg.Links = append(msg.Links, messaging.Link{Label: fmt.Sprint("Destination ", i), URL: fmt.Sprintf("https://example.org/%d", i)})
			}
			if provider == messaging.ProviderWhatsApp {
				client, err := NewClient("token", "123", WithBaseURL(server.URL))
				if err != nil {
					t.Fatal(err)
				}
				if err = client.Send(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
			} else {
				client, err := newDirectClient("token", "123", provider, WithBaseURL(server.URL))
				if err != nil {
					t.Fatal(err)
				}
				if err = client.Send(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(payloads)
			for _, link := range msg.Links {
				if !strings.Contains(string(raw), link.URL) {
					t.Fatalf("destination lost %s: %s", link.URL, raw)
				}
			}
			for _, choice := range msg.Choices {
				if !strings.Contains(string(raw), choice.Label) {
					t.Fatalf("choice lost %s: %s", choice.Label, raw)
				}
			}
			if provider == messaging.ProviderWhatsApp {
				last := payloads[len(payloads)-1]
				if last["type"] != "interactive" {
					t.Fatal("WhatsApp links replaced interactive menu")
				}
			}
		})
	}
}
