package meta

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestNativeChoiceRoundTrip(t *testing.T) {
	label := "Դեմքի մերսում՝ Classic 60 min"
	msg := messaging.Outgoing{ChoiceToken: "nonce", Text: "Pick", Choices: []messaging.Choice{{Label: label}}}
	replies := quickReplies(msg)
	if len(replies) != 1 || utf8.RuneCountInString(replies[0].Title) > 20 {
		t.Fatalf("replies: %+v", replies)
	}
	if strings.HasPrefix(replies[0].Title, "1.") {
		t.Fatalf("native button redundantly numbered: %q", replies[0].Title)
	}
	for _, provider := range []messaging.Provider{messaging.ProviderMessenger, messaging.ProviderInstagram} {
		object := "page"
		if provider == messaging.ProviderInstagram {
			object = "instagram"
		}
		event := strings.Replace(directEvent, `"text":"Можно на русском?"`, fmt.Sprintf(`"text":"truncated", "quick_reply":{"payload":%q}`, replies[0].Payload), 1)
		got, err := parseDirect(directUpdate(object, "business-1", event), receivedAt, provider, "business-1")
		if err != nil || len(got) != 1 || got[0].Content.Text != label || got[0].ChoiceMessageID != "nonce" {
			t.Fatalf("roundtrip: %+v %v", got, err)
		}
	}
	var inbound inboundMessage
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"from":"123","id":"message","type":"interactive","interactive":{"type":"list_reply","list_reply":{"id":%q,"title":"truncated"}}}`, replies[0].Payload)), &inbound); err != nil {
		t.Fatal(err)
	}
	got, err := whatsAppEnvelope(inbound, "", receivedAt)
	if err != nil || got.Content.Text != label || got.ChoiceMessageID != "nonce" {
		t.Fatalf("WA roundtrip: %+v %v", got, err)
	}
	token, _ := decodeChoice("garbage")
	if token != "invalid" {
		t.Fatal("unrecognized callback became ordinary customer text")
	}
}

func TestWhatsAppNativeChoiceLimits(t *testing.T) {
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, body)
		_, _ = w.Write([]byte(`{"messages":[{"id":"sent"}]}`))
	}))
	defer server.Close()
	client, err := NewClient("token", "123", WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	msg := messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "user", ChoiceToken: "nonce", Text: strings.Repeat("ա", 2100)}
	for i := 0; i < 10; i++ {
		msg.Choices = append(msg.Choices, messaging.Choice{Label: fmt.Sprintf("Motion treatment %d long label", i)})
	}
	if err := client.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 3 {
		t.Fatalf("sent %d chunks", len(payloads))
	}
	last := payloads[2]
	interactive := last["interactive"].(map[string]any)
	if interactive["type"] != "list" {
		t.Fatal("not a list")
	}
	rows := interactive["action"].(map[string]any)["sections"].([]any)[0].(map[string]any)["rows"].([]any)
	if len(rows) != 10 {
		t.Fatal("lost choices")
	}
	for _, item := range rows {
		row := item.(map[string]any)
		if utf8.RuneCountInString(row["title"].(string)) > 24 || len(row["id"].(string)) > 200 {
			t.Fatal("exceeded provider limits")
		}
		if strings.HasPrefix(row["title"].(string), "1.") {
			t.Fatalf("WhatsApp row redundantly numbered: %+v", row)
		}
	}
	msg.Text = "Choose"
	msg.Choices = msg.Choices[:3]
	if err := client.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if payloads[3]["interactive"].(map[string]any)["type"] != "button" {
		t.Fatal("three choices should be direct reply buttons")
	}
}
