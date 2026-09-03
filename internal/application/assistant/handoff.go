package assistant

import (
	"context"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// handoffTimeout is how long a conversation waits for a colleague before the
// assistant starts answering again.
//
// A request nobody acted on must not silence the assistant permanently. A
// customer left with no reply at all is a worse outcome than an assistant that
// resumes and offers what it can.
const handoffTimeout = 2 * time.Hour

// HandoffReason says why a person is needed, so the notification can be read at
// a glance and the urgent kind is not buried among the ordinary ones.
type HandoffReason string

const (
	// ReasonCustomerAsked means the customer wanted a person, or the assistant
	// judged it could not help safely.
	ReasonCustomerAsked HandoffReason = "customer_asked"

	// ReasonBookingUnresolved means a booking request was sent and its outcome
	// never came back. The appointment may or may not exist, so somebody has to
	// check the calendar before the customer is told anything.
	ReasonBookingUnresolved HandoffReason = "booking_unresolved"
)

// Urgent reports whether the business should act now rather than in due course.
func (r HandoffReason) Urgent() bool { return r == ReasonBookingUnresolved }

// HandoffNotice is everything a colleague needs to pick a conversation up
// without reading a database.
type HandoffNotice struct {
	ConversationID string
	Reason         HandoffReason

	// Detail is the explanation recorded at the moment of handover, in the
	// words of whoever asked for it.
	Detail string

	Provider messaging.Provider
	Customer customer.Customer

	// Handle is how to reach the customer on their channel when no phone
	// number is known, such as a Telegram username.
	Handle string

	// ExternalUserID is the customer's account on the channel. It is the only
	// reliable way to open a conversation with somebody who has no username and
	// has given no phone number, which is the common case.
	ExternalUserID string

	// Recent is the tail of the conversation, oldest first, so the colleague
	// can see what was already said rather than making the customer repeat it.
	Recent []conversation.Message

	// Draft is the booking that was in flight, when there was one. For an
	// unresolved booking it is the only record of what to look for.
	Draft *booking.Draft

	RequestedAt time.Time
}

// StaffNotifier tells the business that a conversation needs a person.
//
// Without one, a handover changes a stored state and nothing else: the customer
// waits, and nobody ever learns they are waiting.
type StaffNotifier interface {
	NotifyHandoff(ctx context.Context, notice HandoffNotice) error
}

// notifyStaff reports a conversation that needs a person.
//
// Failure is logged rather than returned. The customer has already been told a
// colleague will follow up, and failing the delivery here would have the
// provider redeliver the message and repeat that promise.
func (s *Service) notifyStaff(ctx context.Context, notice HandoffNotice) {
	if s.staff == nil {
		s.logger.WarnContext(ctx, "a conversation needs a person but no staff channel is configured",
			"conversation_id", notice.ConversationID,
			"reason", string(notice.Reason),
			"customer_phone", notice.Customer.Phone,
		)
		return
	}

	if err := s.staff.NotifyHandoff(ctx, notice); err != nil {
		// Logged at error with everything a person would need, so the request
		// is recoverable from the log alone if the staff channel is down.
		s.logger.ErrorContext(ctx, "could not tell the staff a conversation needs a person",
			"error", err,
			"conversation_id", notice.ConversationID,
			"reason", string(notice.Reason),
			"customer_name", notice.Customer.Name,
			"customer_phone", notice.Customer.Phone,
		)
		return
	}

	s.logger.InfoContext(ctx, "told the staff a conversation needs a person",
		"conversation_id", notice.ConversationID,
		"reason", string(notice.Reason),
		"urgent", notice.Reason.Urgent(),
	)
}

// Resume hands a conversation back to the assistant.
//
// It is what a colleague calls when they have finished with a customer, and
// what the timeout calls when nobody ever arrived.
func (s *Service) Resume(ctx context.Context, conversationID string) error {
	conv, err := s.conversations.FindByID(ctx, conversationID)
	if err != nil {
		return err
	}

	if conv.State == conversation.StateAssistantActive {
		return nil
	}

	if err := conv.TransitionTo(conversation.StateAssistantActive, s.now()); err != nil {
		return err
	}
	return s.conversations.Save(ctx, conv)
}
