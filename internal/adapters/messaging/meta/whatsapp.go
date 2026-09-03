package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// ParseWhatsApp normalises a WhatsApp webhook delivery into messages.
//
// A delivery can carry several entries and several changes, and most carry no
// customer message at all: delivery receipts and read receipts arrive on the
// same webhook. Those yield nothing and are not failures.
func ParseWhatsApp(body []byte, receivedAt time.Time) ([]messaging.Envelope, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}

	var envelopes []messaging.Envelope

	for _, e := range u.Entry {
		for _, c := range e.Changes {
			// Names of the sender, keyed by their WhatsApp id. WhatsApp sends
			// the profile name separately from the message.
			names := make(map[string]string, len(c.Value.Contacts))
			for _, contact := range c.Value.Contacts {
				names[contact.WaID] = contact.Profile.Name
			}

			for _, m := range c.Value.Messages {
				envelope, err := whatsAppEnvelope(m, names[m.From], receivedAt)
				if err != nil {
					return nil, err
				}
				envelopes = append(envelopes, envelope)
			}
		}
	}

	return envelopes, nil
}

func whatsAppEnvelope(
	m inboundMessage,
	profileName string,
	receivedAt time.Time,
) (messaging.Envelope, error) {
	if m.From == "" || m.ID == "" {
		return messaging.Envelope{}, fmt.Errorf("%w: message has no sender or id", ErrMalformedUpdate)
	}

	// WhatsApp sends unix seconds as a string. An unreadable timestamp is not
	// worth losing the message over: only ReceivedAt is relied on anyway.
	sentAt := receivedAt
	if seconds, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil {
		sentAt = time.Unix(seconds, 0).UTC()
	}

	envelope := messaging.Envelope{
		Provider:          messaging.ProviderWhatsApp,
		ExternalMessageID: m.ID,
		ExternalUserID:    m.From,

		// The customer's own number is the conversation: WhatsApp has no
		// separate thread identifier.
		ExternalThreadID: m.From,

		SentAt:     sentAt,
		ReceivedAt: receivedAt,
		Sender:     messaging.Sender{DisplayName: profileName},
		Content:    whatsAppContent(m),
	}

	if err := envelope.Validate(); err != nil {
		return messaging.Envelope{}, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	return envelope, nil
}

// whatsAppContent classifies what the customer sent. A caption is treated as
// text, because a photo captioned "can I book this?" carries the request in the
// caption.
func whatsAppContent(m inboundMessage) messaging.Content {
	if m.Text.Body != "" {
		return messaging.Content{Type: messaging.ContentTypeText, Text: m.Text.Body}
	}

	for _, withCaption := range []*mediaWithCaption{m.Image, m.Video, m.Document} {
		if withCaption != nil && withCaption.Caption != "" {
			return messaging.Content{Type: messaging.ContentTypeText, Text: withCaption.Caption}
		}
	}

	return messaging.Content{
		Type:        messaging.ContentTypeUnsupported,
		Description: describeWhatsAppAttachment(m),
	}
}

func describeWhatsAppAttachment(m inboundMessage) string {
	switch {
	case m.Image != nil:
		return "photo"
	case m.Voice != nil:
		return "voice message"
	case m.Audio != nil:
		return "audio file"
	case m.Video != nil:
		return "video"
	case m.Sticker != nil:
		return "sticker"
	case m.Document != nil:
		return "file"
	case m.Location != nil:
		return "location"
	case len(m.Contacts) > 0:
		return "contact"
	default:
		return "message"
	}
}

// SendWhatsApp delivers a text message through the Cloud API.
func (c *Client) SendWhatsApp(ctx context.Context, msg messaging.Outgoing) error {
	if msg.Provider != messaging.ProviderWhatsApp {
		return fmt.Errorf("whatsapp client cannot deliver to %s", msg.Provider)
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	if c.phoneNumberID == "" {
		return fmt.Errorf("whatsapp: no phone number id is configured")
	}

	payload := sendTextRequest{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               msg.ExternalThreadID,
		Type:             "text",
	}
	// Link previews are off: a preview card of whatever a service name happens
	// to resemble is noise in a booking conversation.
	payload.Text.PreviewURL = false
	payload.Text.Body = msg.Text

	return c.post(ctx, c.phoneNumberID+"/messages", payload)
}

// Send makes the client satisfy the application's Sender port for WhatsApp.
func (c *Client) Send(ctx context.Context, msg messaging.Outgoing) error {
	return c.SendWhatsApp(ctx, msg)
}
