// Package conversation models an ongoing exchange with one customer on one
// channel, and the rules governing who is answering it.
package conversation

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// ErrNotFound reports a conversation that does not exist.
var ErrNotFound = errors.New("conversation not found")

// ErrInvalidTransition reports a state change the rules do not allow.
var ErrInvalidTransition = errors.New("invalid conversation state transition")

// ErrExternalReplyConflict means a staff message arrived after this copy was
// loaded. An older assistant turn must not overwrite the takeover or send.
var ErrExternalReplyConflict = errors.New("conversation changed by external staff reply")

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

	// ExternalReplyRevision counts staff messages sent outside the bot, such
	// as from the WhatsApp Business app. A copy loaded before it moved cannot
	// be saved, which stops an assistant turn already in flight from replying
	// over a colleague or undoing their takeover.
	ExternalReplyRevision int64

	// AssistantResumedAt is when the assistant was last put back in charge. A
	// staff message sent before it is transcript history, not a new takeover.
	AssistantResumedAt time.Time

	// Draft is the booking the customer has been shown and not yet confirmed.
	// It lives on the conversation because that is its whole lifetime: it is
	// built during one exchange and either confirmed or abandoned in it.
	Draft *booking.Draft

	// HandoffAt is when the conversation was last passed to a colleague. It is
	// kept separately from UpdatedAt, which moves on every message, so that
	// "how long has this customer been waiting for a person" stays answerable.
	HandoffAt time.Time

	// BookingChange is a cancellation or reschedule the customer has been
	// shown and not yet confirmed. Like Draft, it makes the confirmation step
	// consume stored facts rather than model-supplied arguments.
	BookingChange *booking.ChangeDraft

	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastMessageAt time.Time

	// LastChoiceMessageID identifies the current provider message with buttons.
	// Persisting it allows any service instance to retire an answered keyboard.
	LastChoiceMessageID string

	// A failed reply may retry the same consumed callback, but a different tap
	// on that old keyboard must not act on a newly prepared draft.
	PendingChoiceMessageID string
	PendingChoiceEventID   string

	// PresentedChoices are the numbered options in the latest successful
	// assistant reply. They may outnumber a channel's buttons, and let a
	// customer answer a scan-friendly list with just "2" on every channel.
	PresentedChoices []string
	// Catalogue navigation survives restarts and is separate from booking drafts.
	CatalogueCategory  string
	CatalogueServiceID string
	CataloguePage      int
	ReminderOptIn      bool
}

// ResolvePresentedChoice translates a bare one-based number into the option
// the customer was last shown. Longer messages are left untouched: a phone
// number or a sentence beginning with a digit is not a menu selection.
func (c Conversation) ResolvePresentedChoice(text string) (string, bool) {
	selection := strings.TrimSpace(text)
	for _, label := range c.PresentedChoices {
		if strings.EqualFold(selection, strings.TrimSpace(label)) {
			return strings.TrimSpace(label), true
		}
	}

	selection = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(selection, "."), ")"))
	if selection == "" || strings.ContainsAny(selection, " \t\r\n") {
		return "", false
	}
	position, err := strconv.Atoi(selection)
	if err != nil || position < 1 || position > len(c.PresentedChoices) {
		return "", false
	}
	label := strings.TrimSpace(c.PresentedChoices[position-1])
	return label, label != ""
}

// Key is the unique address of a conversation across all channels.
func Key(provider messaging.Provider, externalThreadID string) string {
	return string(provider) + ":" + externalThreadID
}

// Key is the unique address of this conversation.
func (c Conversation) Key() string {
	return Key(c.Provider, c.ExternalThreadID)
}

// WaitingForHumanLongerThan reports whether a colleague was asked for and has
// still not picked the conversation up after d.
//
// It exists so that a request nobody acted on cannot silence the assistant
// permanently. A customer left with no reply at all is a worse outcome than an
// assistant that starts helping again.
func (c Conversation) WaitingForHumanLongerThan(d time.Duration, now time.Time) bool {
	if c.State != StateHumanRequested {
		return false
	}

	// A conversation waiting with no recorded start time was stored before that
	// time was recorded at all. Treating it as expired is deliberate: such a
	// record can only have come from the behaviour this check exists to undo,
	// where a handover was never announced to anyone and the customer was left
	// talking to nobody. Resuming is the safe reading.
	if c.HandoffAt.IsZero() {
		return true
	}

	return now.Sub(c.HandoffAt) > d
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
			if next == StateAssistantActive {
				c.AssistantResumedAt = at
			}

			if next == StateHumanRequested {
				c.HandoffAt = at
			}
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
