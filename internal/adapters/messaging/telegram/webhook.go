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

// Callback is one press of a button this system offered.
//
// It is not a message, but it is an answer, so it becomes one: the label the
// customer tapped is fed into the conversation exactly as though they had typed
// it. Nothing above this package has to know a button was involved.
type Callback struct {
	// QueryID identifies the press. Telegram shows a spinner on the button
	// until it is acknowledged with this, and it is unique per press, which is
	// also what makes a redelivered press safe to deduplicate.
	QueryID string

	// ChatID and MessageID address the message the button sits under, so the
	// keyboard can be taken away once it has been used.
	ChatID    string
	MessageID int64

	// Data is what the button carried: a position in the keyboard for a
	// customer's answer, or an action and a conversation for a colleague's.
	Data string

	// Envelope is the press expressed as a customer message. It is valid only
	// when Understood is true.
	Envelope messaging.Envelope

	// Understood reports whether the pressed button could be read back. A
	// keyboard Telegram no longer sends the markup for, or one left over from a
	// message this deployment did not send, cannot be, and the customer is told
	// so rather than left with a button that does nothing.
	Understood bool
}

// ParseCallback reads a button press out of a delivery, reporting nil for a
// delivery that is not one.
func (w *Webhook) ParseCallback(body []byte) (*Callback, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	if u.CallbackQuery == nil {
		return nil, nil
	}

	query := u.CallbackQuery
	if query.ID == "" || query.From == nil || query.Message == nil || query.Message.Chat == nil {
		return nil, fmt.Errorf("%w: callback query is missing its sender or message", ErrMalformedUpdate)
	}

	now := w.now().UTC()
	callback := &Callback{
		QueryID:   query.ID,
		ChatID:    strconv.FormatInt(query.Message.Chat.ID, 10),
		MessageID: query.Message.MessageID,
		Data:      query.Data,
	}

	label, ok := labelForCallback(query.Message.ReplyMarkup, query.Data)
	if !ok {
		return callback, nil
	}

	envelope := messaging.Envelope{
		Provider: messaging.ProviderTelegram,
		// The query id, not the message id: the message is the assistant's own
		// and would collide with itself on every press of the same keyboard.
		ExternalMessageID: "callback:" + query.ID,
		ExternalUserID:    strconv.FormatInt(query.From.ID, 10),
		ExternalThreadID:  callback.ChatID,
		SentAt:            now,
		ReceivedAt:        now,
		Sender: messaging.Sender{
			DisplayName: displayName(query.From),
			Language:    query.From.LanguageCode,
			Username:    query.From.Username,
		},
		Content: messaging.Content{Type: messaging.ContentTypeText, Text: label},
	}
	if err := envelope.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}

	callback.Envelope = envelope
	callback.Understood = true
	return callback, nil
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
			Username:    m.From.Username,
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
