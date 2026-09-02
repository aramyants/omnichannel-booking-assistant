// Package conversation models an ongoing exchange with one customer on one
// channel, and the rules governing who is answering it.
package conversation

import (
	"errors"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// ErrNotFound reports a conversation that does not exist.
var ErrNotFound = errors.New("conversation not found")

// ErrInvalidTransition reports a state change the rules do not allow.
var ErrInvalidTransition = errors.New("invalid conversation state transition")

// State records who is answering a conversation.
//
// This is application state, not a prompt instruction. Whether the assistant
// may reply is decided here, in code, so that no phrasing in a customer's
// message can talk the system into answering a conversation a colleague has
// taken over.
type State string

const (
	// StateAssistantActive means the assistant answers.
	StateAssistantActive State = "assistant_active"

	// StateHumanRequested means a handover has been asked for but no colleague
	// has picked it up. The assistant stops replying immediately, rather than
	// continuing until someone arrives.
	StateHumanRequested State = "human_requested"

	// StateHumanActive means a colleague is answering.
	StateHumanActive State = "human_active"

	// StateClosed means the exchange is finished. A new message reopens it.
	StateClosed State = "closed"
)

// Conversation is one exchange with one customer on one channel.
type Conversation struct {
	ID         string
	CustomerID string
	Provider   messaging.Provider

	// ExternalThreadID is the provider's own conversation identifier, and the
	// address replies are delivered to.
	ExternalThreadID string

	State State

	// Draft is the booking the customer has been shown and not yet confirmed.
	// It lives on the conversation because that is its whole lifetime: it is
	// built during one exchange and either confirmed or abandoned in it.
	Draft *booking.Draft

	// BookingChange is a cancellation or reschedule the customer has been
	// shown and not yet confirmed. Like Draft, it makes the confirmation step
	// consume stored facts rather than model-supplied arguments.
	BookingChange *booking.ChangeDraft

	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastMessageAt time.Time
}

// Key is the unique address of a conversation across all channels.
func Key(provider messaging.Provider, externalThreadID string) string {
	return string(provider) + ":" + externalThreadID
}

// Key is the unique address of this conversation.
func (c Conversation) Key() string {
	return Key(c.Provider, c.ExternalThreadID)
}

// AssistantMayReply reports whether the assistant should answer.
//
// A colleague who has taken a conversation over must not have the assistant
// talking over them, and a customer waiting for a person must not be answered
// by the bot again.
func (c Conversation) AssistantMayReply() bool {
	return c.State == StateAssistantActive
}

// allowedTransitions lists the states each state may move to.
var allowedTransitions = map[State][]State{
	StateAssistantActive: {StateHumanRequested, StateHumanActive, StateClosed},
	StateHumanRequested:  {StateHumanActive, StateAssistantActive, StateClosed},
	StateHumanActive:     {StateAssistantActive, StateClosed},
	StateClosed:          {StateAssistantActive},
}

// TransitionTo moves the conversation to next, or reports why it cannot.
//
// Moving to the current state is allowed and does nothing, so a caller reacting
// to a repeated request does not have to check first.
func (c *Conversation) TransitionTo(next State, at time.Time) error {
	if c.State == next {
		return nil
	}

	for _, allowed := range allowedTransitions[c.State] {
		if allowed == next {
			c.State = next
			c.UpdatedAt = at
			return nil
		}
	}

	return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, c.State, next)
}

// Direction records whether a stored message came from the customer or went to
// them.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Message is one stored message in a conversation.
//
// The transcript is the record of what was actually said. It holds customer
// messages and the replies they were sent, and nothing else: no hidden model
// reasoning, no internal deliberation. Only what a person could have read.
type Message struct {
	ID             string
	ConversationID string
	Direction      Direction
	ContentType    messaging.ContentType
	Text           string

	// ExternalMessageID is the provider's identifier for an inbound message,
	// and empty for replies this system generated.
	ExternalMessageID string

	CreatedAt time.Time
}
