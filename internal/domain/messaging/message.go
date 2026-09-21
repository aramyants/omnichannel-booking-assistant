// Package messaging holds the channel-independent representation of customer
// messages. Nothing in this package knows how any particular provider encodes
// a message; adapters translate into and out of these types at the boundary.
package messaging

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Provider identifies the channel a message travelled over.
type Provider string

const (
	ProviderTelegram  Provider = "telegram"
	ProviderWhatsApp  Provider = "whatsapp"
	ProviderInstagram Provider = "instagram"
	ProviderMessenger Provider = "messenger"
	ProviderViber     Provider = "viber"
)

// ContentType describes what a customer actually sent. Anything the assistant
// cannot read is ContentTypeUnsupported rather than a silently empty message,
// so the conversation can tell the customer what happened.
type ContentType string

const (
	ContentTypeText        ContentType = "text"
	ContentTypeAudio       ContentType = "audio"
	ContentTypeUnsupported ContentType = "unsupported"
)

// Content is the body of an incoming message.
type Content struct {
	Type  ContentType
	Text  string
	Audio *Audio

	// Description names the kind of unsupported content, such as "photo" or
	// "voice", so a reply can be specific about what was ignored.
	Description string
}

// Audio identifies provider-hosted input. Bytes and URLs are never sent to the
// conversation model or stored in the transcript.
type Audio struct {
	Reference       string
	MIMEType        string
	Filename        string
	SizeBytes       int64
	DurationSeconds int
}

const MaxAudioBytes = 20 << 20

// Sender carries the profile information a provider volunteers about the
// person who sent a message. All of it is optional and none of it is trusted
// as identity: it is display and language data only.
type Sender struct {
	DisplayName string
	Language    string

	// Username is the handle the customer can be reached at on their channel,
	// when they have one. It is how a colleague opens a conversation with them
	// after a handover, since messaging providers do not give out phone numbers.
	Username string
}

// Envelope is a single inbound customer message, normalised across channels.
//
// Provider-specific payloads never travel further into the system than the
// adapter that produced this value.
type Envelope struct {
	Provider Provider

	// ExternalMessageID is the provider's own identifier for this message. It
	// is the basis of deduplication: providers retry deliveries, so the same
	// message can and will arrive more than once.
	ExternalMessageID string

	// ChoiceMessageID identifies the bot message a button answered, when the
	// channel supplies it. Old keyboards cannot confirm a newer booking draft.
	ChoiceMessageID string

	// ExternalUserID identifies the sender within the provider. It is not a
	// customer identity; one person may hold several of these across channels.
	ExternalUserID string

	// ExternalThreadID identifies the conversation to reply into.
	ExternalThreadID string

	// SentAt is when the provider says the customer sent the message.
	SentAt time.Time

	// ReceivedAt is when this system accepted it. The two differ under retry
	// and provider delay, and only ReceivedAt is trustworthy.
	ReceivedAt time.Time

	Sender  Sender
	Content Content
}

// ErrInvalidEnvelope reports a normalised message that cannot be processed.
var ErrInvalidEnvelope = errors.New("invalid message envelope")

// Validate reports whether the envelope carries the identifiers every later
// stage depends on. Adapters call this before handing a message onward, so a
// malformed provider payload fails at the boundary rather than deep inside
// conversation handling.
func (e Envelope) Validate() error {
	switch {
	case e.Provider == "":
		return fmt.Errorf("%w: provider is empty", ErrInvalidEnvelope)
	case e.ExternalMessageID == "":
		return fmt.Errorf("%w: external message id is empty", ErrInvalidEnvelope)
	case e.ExternalUserID == "":
		return fmt.Errorf("%w: external user id is empty", ErrInvalidEnvelope)
	case e.ExternalThreadID == "":
		return fmt.Errorf("%w: external thread id is empty", ErrInvalidEnvelope)
	case e.Content.Type == "":
		return fmt.Errorf("%w: content type is empty", ErrInvalidEnvelope)
	}
	return nil
}

// DedupeKey is the stable identity of an inbound message. Two deliveries of
// the same provider message produce the same key, which is what lets a repeat
// delivery be dropped rather than answered twice.
func (e Envelope) DedupeKey() string {
	// Telegram message_id is unique only inside its chat. Different customers
	// routinely send messages with the same number.
	if e.Provider == ProviderTelegram {
		return string(e.Provider) + ":" + e.ExternalThreadID + ":" + e.ExternalMessageID
	}
	return string(e.Provider) + ":" + e.ExternalMessageID
}

// Choice is one tappable option offered alongside a reply.
//
// The label is the whole of it. Whatever a customer taps is fed back into the
// conversation exactly as though they had typed it, so a channel with buttons
// and a channel without behave identically above this line, and nothing has to
// be remembered between offering a choice and receiving the answer.
type Choice struct {
	Label string
}

// Link is a safe, tappable action that opens a customer-facing HTTPS page.
// Its URL also remains in the message text as a fallback for clients that do
// not render structured link buttons.
type Link struct {
	Label string
	URL   string
}

// maxChoices bounds how many options one reply may carry. More than this is a
// wall rather than a choice, and no phone shows it without scrolling.
const maxChoices = 12

// Outgoing is a message the assistant wants to deliver back to a customer.
type Outgoing struct {
	ChoiceToken string
	Provider    Provider

	// ExternalThreadID is the conversation to deliver into, taken from the
	// envelope being answered.
	ExternalThreadID string

	Text string

	// Choices are options to offer as buttons. A channel that cannot render
	// them ignores the field: the reply text names the options either way, so
	// a customer there simply types their answer instead of tapping it.
	Choices []Choice
	Links   []Link
}

// WithChoices returns a copy of o offering choices, dropping the empty and the
// repeated and keeping no more than a customer can be shown at once.
//
// Repeats are dropped because two identical buttons are indistinguishable once
// tapped, so the second one can only ever confuse.
func (o Outgoing) WithChoices(choices []Choice) Outgoing {
	kept := make([]Choice, 0, len(choices))
	seen := make(map[string]struct{}, len(choices))

	for _, choice := range choices {
		label := strings.TrimSpace(choice.Label)
		if label == "" {
			continue
		}
		if _, duplicate := seen[label]; duplicate {
			continue
		}
		seen[label] = struct{}{}

		kept = append(kept, Choice{Label: label})
		if len(kept) == maxChoices {
			break
		}
	}

	if len(kept) == 0 {
		o.Choices = nil
		return o
	}
	o.Choices = kept
	return o
}

// WithLinks returns a copy of o with distinct, non-empty HTTPS actions.
func (o Outgoing) WithLinks(links []Link) Outgoing {
	kept := make([]Link, 0, len(links))
	seen := make(map[string]bool, len(links))
	for _, link := range links {
		label, rawURL := strings.TrimSpace(link.Label), strings.TrimSpace(link.URL)
		if label == "" || !strings.HasPrefix(strings.ToLower(rawURL), "https://") || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		kept = append(kept, Link{Label: label, URL: rawURL})
		if len(kept) == 6 {
			break
		}
	}
	o.Links = kept
	return o
}

// Validate reports whether the message can be delivered.
func (o Outgoing) Validate() error {
	switch {
	case o.Provider == "":
		return fmt.Errorf("%w: provider is empty", ErrInvalidEnvelope)
	case o.ExternalThreadID == "":
		return fmt.Errorf("%w: external thread id is empty", ErrInvalidEnvelope)
	case o.Text == "":
		return fmt.Errorf("%w: text is empty", ErrInvalidEnvelope)
	}
	return nil
}

// Reply builds a message addressed to the same conversation as e.
func (e Envelope) Reply(text string) Outgoing {
	return Outgoing{
		Provider:         e.Provider,
		ExternalThreadID: e.ExternalThreadID,
		Text:             text,
	}
}
