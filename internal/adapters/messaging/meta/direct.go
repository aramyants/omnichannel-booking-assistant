package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// DirectClient sends replies for one Facebook Page or Instagram professional
// account. Instagram uses Instagram Login tokens and graph.instagram.com;
// Messenger uses Page tokens and graph.facebook.com.
type DirectClient struct {
	client    *Client
	provider  messaging.Provider
	accountID string
}

func NewMessengerClient(accessToken, pageID string, opts ...Option) (*DirectClient, error) {
	return newDirectClient(accessToken, pageID, messaging.ProviderMessenger, opts...)
}

func NewInstagramClient(accessToken, accountID string, opts ...Option) (*DirectClient, error) {
	return newDirectClient(accessToken, accountID, messaging.ProviderInstagram, opts...)
}

func newDirectClient(accessToken, accountID string, provider messaging.Provider, opts ...Option) (*DirectClient, error) {
	if accountID == "" {
		return nil, fmt.Errorf("%s: account id is required", provider)
	}
	if provider == messaging.ProviderInstagram {
		opts = append([]Option{WithBaseURL("https://graph.instagram.com")}, opts...)
	}
	c, err := NewClient(accessToken, "", opts...)
	if err != nil {
		return nil, err
	}
	return &DirectClient{client: c, provider: provider, accountID: accountID}, nil
}

func (c *DirectClient) Send(ctx context.Context, msg messaging.Outgoing) error {
	if msg.Provider != c.provider {
		return fmt.Errorf("%s client cannot deliver to %s", c.provider, msg.Provider)
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	payload := directTextRequest{Recipient: party{ID: msg.ExternalThreadID}}
	payload.Message.Text = msg.Text
	if c.provider == messaging.ProviderMessenger {
		payload.MessagingType = "RESPONSE"
	}
	return c.client.post(ctx, c.accountID+"/messages", payload)
}

func NewMessengerHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, pageID string) *Handler {
	return newDirectHandler(webhook, messages, logger, messaging.ProviderMessenger, pageID)
}

func NewInstagramHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, accountID string) *Handler {
	return newDirectHandler(webhook, messages, logger, messaging.ProviderInstagram, accountID)
}

func newDirectHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, provider messaging.Provider, accountID string) *Handler {
	return &Handler{
		webhook: webhook, messages: messages, logger: logger, now: time.Now,
		parse: func(body []byte, receivedAt time.Time) ([]messaging.Envelope, error) {
			return parseDirect(body, receivedAt, provider, accountID)
		},
	}
}

func parseDirect(body []byte, receivedAt time.Time, provider messaging.Provider, accountID string) ([]messaging.Envelope, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	object := "page"
	if provider == messaging.ProviderInstagram {
		object = "instagram"
	}
	if u.Object != object || accountID == "" {
		return nil, nil
	}
	var envelopes []messaging.Envelope
	for _, entry := range u.Entry {
		// One app can subscribe to multiple assets. A valid app signature does
		// not mean a message belongs to the business this deployment serves.
		if entry.ID != accountID {
			continue
		}
		for _, event := range entry.Messaging {
			m := event.Message
			if m == nil || m.IsEcho || m.IsDeleted || event.Sender.ID == accountID || event.Recipient.ID != accountID {
				continue
			}
			content := messaging.Content{Type: messaging.ContentTypeText, Text: m.Text}
			if len(m.Attachments) > 0 && m.Attachments[0].Type == "audio" && m.Attachments[0].Payload.URL != "" {
				content = messaging.Content{Type: messaging.ContentTypeAudio, Text: m.Text, Description: "voice message", Audio: &messaging.Audio{Reference: m.Attachments[0].Payload.URL}}
			}
			if m.Text == "" && content.Type != messaging.ContentTypeAudio {
				content = messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "attachment"}
				if len(m.Attachments) > 0 {
					switch m.Attachments[0].Type {
					case "image":
						content.Description = "photo"
					case "audio":
						content.Description = "voice message"
					case "video":
						content.Description = "video"
					case "file":
						content.Description = "file"
					case "share", "ig_reel", "reel":
						content.Description = "shared post"
					}
				}
			}
			sentAt := receivedAt
			if event.Timestamp > 0 {
				sentAt = time.UnixMilli(event.Timestamp).UTC()
			}
			envelope := messaging.Envelope{
				Provider: provider, ExternalMessageID: m.MID,
				ExternalUserID: event.Sender.ID, ExternalThreadID: event.Sender.ID,
				SentAt: sentAt, ReceivedAt: receivedAt, Content: content,
			}
			if err := envelope.Validate(); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
			}
			envelopes = append(envelopes, envelope)
		}
	}
	return envelopes, nil
}

type directTextRequest struct {
	Recipient     party  `json:"recipient"`
	MessagingType string `json:"messaging_type,omitempty"`
	Message       struct {
		Text string `json:"text"`
	} `json:"message"`
}
