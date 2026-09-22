package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
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
	profiles  sync.Map
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
	messageType := ""
	if c.provider == messaging.ProviderMessenger {
		messageType = "RESPONSE"
	}
	limit := 1000
	if len(msg.Links) > 0 || len(msg.Choices) > 0 {
		// Meta's button template keeps URLs tappable in Instagram and Messenger;
		// its text field is smaller than a plain message.
		limit = 640
	}
	chunks := textChunks(msg.Text, limit)
	for i, chunk := range chunks {
		if i == len(chunks)-1 && len(msg.Links) > 0 {
			payload := directButtonRequest{Recipient: party{ID: msg.ExternalThreadID}, MessagingType: messageType}
			payload.Message.Attachment.Type = "template"
			payload.Message.Attachment.Payload.TemplateType = "button"
			payload.Message.Attachment.Payload.Text = chunk
			for _, link := range msg.Links {
				payload.Message.Attachment.Payload.Buttons = append(payload.Message.Attachment.Payload.Buttons, directButton{
					Type: "web_url", URL: link.URL, Title: shortened(link.Label, 20),
				})
				if len(payload.Message.Attachment.Payload.Buttons) == 3 {
					break
				}
			}
			if err := c.client.post(ctx, c.accountID+"/messages", payload); err != nil {
				return err
			}
			continue
		}
		if i == len(chunks)-1 && len(msg.Choices) > 0 {
			buttons := directChoiceButtons(msg)
			for start := 0; start < len(buttons); start += 3 {
				end := min(start+3, len(buttons))
				payload := directButtonRequest{Recipient: party{ID: msg.ExternalThreadID}, MessagingType: messageType}
				payload.Message.Attachment.Type = "template"
				payload.Message.Attachment.Payload.TemplateType = "button"
				payload.Message.Attachment.Payload.Text = "\u21b3"
				if start == 0 {
					payload.Message.Attachment.Payload.Text = chunk
				}
				payload.Message.Attachment.Payload.Buttons = buttons[start:end]
				if err := c.client.post(ctx, c.accountID+"/messages", payload); err != nil {
					return err
				}
			}
			if len(buttons) > 0 {
				continue
			}
		}
		payload := directTextRequest{Recipient: party{ID: msg.ExternalThreadID}, MessagingType: messageType}
		payload.Message.Text = chunk
		if i == len(chunks)-1 && len(msg.Choices) > 0 {
			payload.Message.QuickReplies = quickReplies(msg)
		}
		if err := c.client.post(ctx, c.accountID+"/messages", payload); err != nil {
			return err
		}
	}
	return nil
}

// BeginFeedback displays Meta's native typing state while the assistant is
// working. It is best effort at the handler boundary and never decides whether
// the customer message itself is acknowledged.
func (c *DirectClient) BeginFeedback(ctx context.Context, msg messaging.Envelope) error {
	return c.sendAction(ctx, msg.ExternalThreadID, "typing_on")
}

func (c *DirectClient) EndFeedback(ctx context.Context, msg messaging.Envelope) error {
	return c.sendAction(ctx, msg.ExternalThreadID, "typing_off")
}

// ResolveProfile retrieves the name and handle Meta exposes for the person who
// has opened this conversation. It is cached per instance and fails open: a
// missing permission must never stop the message itself from being answered.
func (c *DirectClient) ResolveProfile(ctx context.Context, msg messaging.Envelope) (messaging.Sender, error) {
	if cached, ok := c.profiles.Load(msg.ExternalUserID); ok {
		return cached.(messaging.Sender), nil
	}
	fields := "name,username"
	if c.provider == messaging.ProviderMessenger {
		fields = "first_name,last_name,locale"
	}
	var profile struct {
		Name      string `json:"name"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Locale    string `json:"locale"`
	}
	if err := c.client.get(ctx, url.PathEscape(msg.ExternalUserID), url.Values{"fields": []string{fields}}, &profile); err != nil {
		if errors.Is(err, ErrRejected) {
			c.profiles.Store(msg.ExternalUserID, messaging.Sender{})
		}
		return messaging.Sender{}, err
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.TrimSpace(strings.Join([]string{profile.FirstName, profile.LastName}, " "))
	}
	if name == "" {
		name = strings.TrimSpace(profile.Username)
	}
	sender := messaging.Sender{DisplayName: name, Username: strings.TrimPrefix(strings.TrimSpace(profile.Username), "@"), Language: strings.ReplaceAll(profile.Locale, "_", "-")}
	if sender.DisplayName != "" || sender.Username != "" || sender.Language != "" {
		c.profiles.Store(msg.ExternalUserID, sender)
	}
	return sender, nil
}

func (c *DirectClient) sendAction(ctx context.Context, threadID, action string) error {
	payload := directActionRequest{Recipient: party{ID: threadID}, SenderAction: action}
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
			if event.Postback != nil && event.Sender.ID != accountID && event.Recipient.ID == accountID {
				token, label := decodeChoice(event.Postback.Payload)
				sentAt := receivedAt
				if event.Timestamp > 0 {
					sentAt = time.UnixMilli(event.Timestamp).UTC()
				}
				messageID := event.Postback.MID
				if messageID == "" {
					messageID = fmt.Sprintf("postback:%s:%d", event.Sender.ID, event.Timestamp)
				}
				envelope := messaging.Envelope{
					Provider: provider, ExternalMessageID: messageID,
					ExternalUserID: event.Sender.ID, ExternalThreadID: event.Sender.ID,
					ChoiceMessageID: token, SentAt: sentAt, ReceivedAt: receivedAt,
					Content: messaging.Content{Type: messaging.ContentTypeText, Text: label},
				}
				if err := envelope.Validate(); err != nil {
					return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
				}
				envelopes = append(envelopes, envelope)
				continue
			}
			if m == nil && event.Reaction != nil && event.Sender.ID != accountID && event.Recipient.ID == accountID {
				reaction := strings.TrimSpace(event.Reaction.Emoji)
				if reaction == "" {
					reaction = strings.TrimSpace(event.Reaction.Reaction)
				}
				text := "[The customer reacted " + reaction + " to a previous message. This is feedback only, not confirmation of a booking or change.]"
				if strings.EqualFold(event.Reaction.Action, "unreact") || strings.EqualFold(event.Reaction.Action, "remove") {
					text = "[The customer removed a reaction from a previous message. This is feedback only, not confirmation of a booking or change.]"
				}
				sentAt := receivedAt
				if event.Timestamp > 0 {
					sentAt = time.UnixMilli(event.Timestamp).UTC()
				}
				envelope := messaging.Envelope{
					Provider: provider, ExternalMessageID: fmt.Sprintf("reaction:%s:%d:%s", event.Reaction.MID, event.Timestamp, event.Reaction.Action),
					ExternalUserID: event.Sender.ID, ExternalThreadID: event.Sender.ID,
					SentAt: sentAt, ReceivedAt: receivedAt, Content: messaging.Content{Type: messaging.ContentTypeText, Text: text},
				}
				if event.Reaction.MID == "" || event.Reaction.Action == "" {
					return nil, fmt.Errorf("%w: reaction is missing its message or action", ErrMalformedUpdate)
				}
				if err := envelope.Validate(); err != nil {
					return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
				}
				envelopes = append(envelopes, envelope)
				continue
			}
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
			if m.QuickReply != nil {
				token, label := decodeChoice(m.QuickReply.Payload)
				envelope.ChoiceMessageID = token
				envelope.Content = messaging.Content{Type: messaging.ContentTypeText, Text: label}
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
		Text         string       `json:"text"`
		QuickReplies []quickReply `json:"quick_replies,omitempty"`
	} `json:"message"`
}

type directActionRequest struct {
	Recipient    party  `json:"recipient"`
	SenderAction string `json:"sender_action"`
}

type directButton struct {
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title"`
	Payload string `json:"payload,omitempty"`
}

func directChoiceButtons(msg messaging.Outgoing) []directButton {
	buttons := make([]directButton, 0, len(msg.Choices))
	for _, choice := range msg.Choices {
		payload := choicePayload(msg.ChoiceToken, choice.Label)
		if len(payload) > 1000 {
			continue
		}
		buttons = append(buttons, directButton{
			Type: "postback", Title: choiceTitle(choice.Label, 20), Payload: payload,
		})
	}
	return buttons
}

type directButtonRequest struct {
	Recipient     party  `json:"recipient"`
	MessagingType string `json:"messaging_type,omitempty"`
	Message       struct {
		Attachment struct {
			Type    string `json:"type"`
			Payload struct {
				TemplateType string         `json:"template_type"`
				Text         string         `json:"text"`
				Buttons      []directButton `json:"buttons"`
			} `json:"payload"`
		} `json:"attachment"`
	} `json:"message"`
}
