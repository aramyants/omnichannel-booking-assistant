// Package messaging holds the channel-independent representation of customer
// messages. Nothing in this package knows how any particular provider encodes
// a message; adapters translate into and out of these types at the boundary.
package messaging

import (
	"errors"
	"fmt"
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
	ContentTypeUnsupported ContentType = "unsupported"
)

// Content is the body of an incoming message.
type Content struct {
	Type ContentType
	Text string

	// Description names the kind of unsupported content, such as "photo" or
	// "voice", so a reply can be specific about what was ignored.
	Description string
}

// Sender carries the profile information a provider volunteers about the
// person who sent a message. All of it is optional and none of it is trusted
// as identity: it is display and language data only.
type Sender struct {
	DisplayName string
	Language    string
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
	return string(e.Provider) + ":" + e.ExternalMessageID
}

// Outgoing is a message the assistant wants to deliver back to a customer.
type Outgoing struct {
	Provider Provider

	// ExternalThreadID is the conversation to deliver into, taken from the
	// envelope being answered.
	ExternalThreadID string

	Text string
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
