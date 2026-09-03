package telegram

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// fixture reads a captured Telegram payload. Parsing is tested against real
// payload shapes rather than structs built in the test, so a change in what
// Telegram actually sends is what breaks the build.
func fixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func testWebhook() *Webhook {
	w := NewWebhook("s3cret-token")
	w.now = func() time.Time { return time.Unix(1756728500, 0).UTC() }
	return w
}

func TestVerifySecret(t *testing.T) {
	w := testWebhook()

	if !w.VerifySecret("s3cret-token") {
		t.Error("the configured secret was rejected")
	}
	for _, wrong := range []string{"", "s3cret-toke", "s3cret-tokenn", "S3CRET-TOKEN", "other"} {
		if w.VerifySecret(wrong) {
			t.Errorf("secret %q was accepted", wrong)
		}
	}
}

func TestParseTextMessage(t *testing.T) {
	envelopes, err := testWebhook().Parse(fixture(t, "text_message.json"))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("parsed %d messages, want 1", len(envelopes))
	}

	got := envelopes[0]
	want := messaging.Envelope{
		Provider:          messaging.ProviderTelegram,
		ExternalMessageID: "4127",
		ExternalUserID:    "219847362",
		ExternalThreadID:  "219847362",
		SentAt:            time.Unix(1756728000, 0).UTC(),
		ReceivedAt:        time.Unix(1756728500, 0).UTC(),
		Sender: messaging.Sender{
			DisplayName: "Anna Petrosyan",
			Language:    "hy",
			// Carried so a colleague can open a chat with the customer after a
			// handover, since Telegram gives out no phone number.
			Username: "annap",
		},
		Content: messaging.Content{
			Type: messaging.ContentTypeText,
			Text: "Hi, can I book a haircut on Friday afternoon?",
		},
	}

	if got != want {
		t.Errorf("envelope mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestParseUsesChatNotSenderForReplies matters in group chats, where replying
// to the sender's own id would send the answer to the wrong conversation.
func TestParseUsesChatNotSenderForReplies(t *testing.T) {
	envelopes, err := testWebhook().Parse(fixture(t, "group_message.json"))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("parsed %d messages, want 1", len(envelopes))
	}

	got := envelopes[0]
	if got.ExternalThreadID != "-1001234567890" {
		t.Errorf("thread = %q, want the group chat id", got.ExternalThreadID)
	}
	if got.ExternalUserID != "219847362" {
		t.Errorf("user = %q, want the sender id", got.ExternalUserID)
	}
}

func TestParseAttachments(t *testing.T) {
	tests := map[string]struct {
		fixture string
		want    messaging.Content
	}{
		"a photo alone cannot be read": {
			fixture: "photo_message.json",
			want:    messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "photo"},
		},
		"a voice message cannot be read": {
			fixture: "voice_message.json",
			want:    messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "voice message"},
		},
		"a caption carries the request": {
			fixture: "photo_with_caption.json",
			want: messaging.Content{
				Type: messaging.ContentTypeText,
				Text: "Can I book this style for Saturday?",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			envelopes, err := testWebhook().Parse(fixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("Parse() returned error: %v", err)
			}
			if len(envelopes) != 1 {
				t.Fatalf("parsed %d messages, want 1", len(envelopes))
			}
			if envelopes[0].Content != tt.want {
				t.Errorf("content = %+v, want %+v", envelopes[0].Content, tt.want)
			}
		})
	}
}

// TestParseIgnoresUpdatesWithNothingToAnswer checks that update types the
// assistant does not act on are not treated as failures. Telegram delivers them
// normally, and answering them with an error would cause endless redelivery.
func TestParseIgnoresUpdatesWithNothingToAnswer(t *testing.T) {
	for _, name := range []string{"edited_message.json", "callback_query.json"} {
		t.Run(name, func(t *testing.T) {
			envelopes, err := testWebhook().Parse(fixture(t, name))
			if err != nil {
				t.Fatalf("Parse() returned error: %v", err)
			}
			if len(envelopes) != 0 {
				t.Errorf("parsed %d messages, want none", len(envelopes))
			}
		})
	}
}

func TestParseRejectsMalformedBodies(t *testing.T) {
	tests := map[string]string{
		"not json":                 "this is not json",
		"message without a chat":   `{"update_id":1,"message":{"message_id":2,"from":{"id":3},"date":4,"text":"hi"}}`,
		"message without a sender": `{"update_id":1,"message":{"message_id":2,"chat":{"id":3},"date":4,"text":"hi"}}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := testWebhook().Parse([]byte(body))
			if err == nil {
				t.Fatal("Parse() accepted a malformed body")
			}
			if !errors.Is(err, ErrMalformedUpdate) {
				t.Errorf("error %v does not wrap ErrMalformedUpdate", err)
			}
		})
	}
}

// TestParseIgnoresUnknownFields proves the parser survives Telegram adding to
// the payload, which it does regularly.
func TestParseIgnoresUnknownFields(t *testing.T) {
	body := `{
		"update_id": 1,
		"some_future_field": {"nested": true},
		"message": {
			"message_id": 2,
			"from": {"id": 3, "first_name": "Anna", "future": 1},
			"chat": {"id": 3, "type": "private"},
			"date": 1756728000,
			"text": "hello"
		}
	}`

	envelopes, err := testWebhook().Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("parsed %d messages, want 1", len(envelopes))
	}
	if envelopes[0].Content.Text != "hello" {
		t.Errorf("text = %q, want %q", envelopes[0].Content.Text, "hello")
	}
}
