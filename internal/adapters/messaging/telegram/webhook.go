// Package telegram adapts the Telegram Bot API to the application's messaging
// ports. Telegram payload shapes do not leave this package.
package telegram

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// SecretHeader is the header Telegram sends the configured webhook secret in.
const SecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // header name, not a credential

// ErrMalformedUpdate reports a webhook body that is not a Telegram update.
var ErrMalformedUpdate = errors.New("malformed telegram update")

// Webhook verifies and parses inbound Telegram webhook deliveries.
type Webhook struct {
	secret string
	now    func() time.Time
}

// NewWebhook returns a Webhook that accepts deliveries carrying secret.
//
// The secret is the value passed to Telegram's setWebhook method. Telegram
// publishes no source IP range and signs nothing, so this shared secret is the
// only thing distinguishing a real delivery from anyone on the internet posting
// to the endpoint.
func NewWebhook(secret string) *Webhook {
	return &Webhook{secret: secret, now: time.Now}
}

// VerifySecret reports whether a delivery carries the configured secret.
//
// The comparison is constant-time. A byte-by-byte comparison returns sooner for
// a wrong first character than a wrong last one, and that timing difference is
// enough to recover the secret one character at a time.
func (w *Webhook) VerifySecret(received string) bool {
	return subtle.ConstantTimeCompare([]byte(received), []byte(w.secret)) == 1
}

// Parse normalises a webhook body into zero or more messages.
//
// Updates the assistant does not act on, such as edits and reactions, yield no
// messages and no error: they are delivered normally by Telegram and are not a
// failure.
func (w *Webhook) Parse(body []byte) ([]messaging.Envelope, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}

	// Edits are ignored for now. Telegram reuses the original message_id, so an
	// edit would be dropped by deduplication anyway; handling it properly needs
	// conversation state that does not exist yet.
	if u.Message == nil {
		return nil, nil
	}

	envelope, err := w.toEnvelope(u.Message)
	if err != nil {
		return nil, err
	}
	return []messaging.Envelope{envelope}, nil
}

func (w *Webhook) toEnvelope(m *message) (messaging.Envelope, error) {
	if m.Chat == nil {
		return messaging.Envelope{}, fmt.Errorf("%w: message has no chat", ErrMalformedUpdate)
	}
	if m.From == nil {
		return messaging.Envelope{}, fmt.Errorf("%w: message has no sender", ErrMalformedUpdate)
	}

	envelope := messaging.Envelope{
		Provider:          messaging.ProviderTelegram,
		ExternalMessageID: strconv.FormatInt(m.MessageID, 10),
		ExternalUserID:    strconv.FormatInt(m.From.ID, 10),
		ExternalThreadID:  strconv.FormatInt(m.Chat.ID, 10),
		SentAt:            time.Unix(m.Date, 0).UTC(),
		ReceivedAt:        w.now().UTC(),
		Sender: messaging.Sender{
			DisplayName: displayName(m.From),
			Language:    m.From.LanguageCode,
		},
		Content: content(m),
	}

	if err := envelope.Validate(); err != nil {
		return messaging.Envelope{}, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	return envelope, nil
}

func displayName(u *user) string {
	switch {
	case u.FirstName != "" && u.LastName != "":
		return u.FirstName + " " + u.LastName
	case u.FirstName != "":
		return u.FirstName
	default:
		return u.Username
	}
}

// content classifies what the customer sent. A caption on an attachment is
// treated as text, because a photo captioned "can I book this style?" carries
// the request in the caption.
func content(m *message) messaging.Content {
	if m.Text != "" {
		return messaging.Content{Type: messaging.ContentTypeText, Text: m.Text}
	}
	if m.Caption != "" {
		return messaging.Content{Type: messaging.ContentTypeText, Text: m.Caption}
	}
	return messaging.Content{
		Type:        messaging.ContentTypeUnsupported,
		Description: describeAttachment(m),
	}
}

func describeAttachment(m *message) string {
	switch {
	case len(m.Photo) > 0:
		return "photo"
	case m.Voice != nil:
		return "voice message"
	case m.Audio != nil:
		return "audio file"
	case m.VideoNote != nil:
		return "video note"
	case m.Video != nil:
		return "video"
	case m.Sticker != nil:
		return "sticker"
	case m.Document != nil:
		return "file"
	case m.Location != nil:
		return "location"
	case m.Contact != nil:
		return "contact"
	default:
		return "message"
	}
}
