// Package assistant turns an incoming customer message into a reply, and keeps
// the record of what was said.
//
// It is the seam the AI orchestrator and the booking workflow will be built
// behind. The replies are still deterministic; what is already real is the
// deduplication, identity resolution and transcript that booking will depend on.
package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

// Sender delivers a message on the channel it names. Each provider adapter
// implements it for its own channel.
type Sender interface {
	Send(ctx context.Context, msg messaging.Outgoing) error
}

// CustomerRepository stores customers and the messaging accounts they write
// from.
type CustomerRepository interface {
	// FindOrCreateByChannelIdentity returns the customer owning identity,
	// creating candidate and linking the identity to it when the identity is
	// not yet known.
	//
	// The two steps are one operation because they race: a customer who sends
	// two messages at once would otherwise be created twice, splitting their
	// history across two records.
	FindOrCreateByChannelIdentity(
		ctx context.Context,
		identity customer.ChannelIdentity,
		candidate customer.Customer,
	) (customer.Customer, error)
}

// ConversationRepository stores conversations.
type ConversationRepository interface {
	// FindOrOpen returns the existing conversation for the channel thread
	// candidate names, or stores and returns candidate if there is none.
	FindOrOpen(ctx context.Context, candidate conversation.Conversation) (conversation.Conversation, error)

	Save(ctx context.Context, conv conversation.Conversation) error
}

// MessageRepository stores the transcript.
type MessageRepository interface {
	Append(ctx context.Context, msg conversation.Message) error

	// Recent returns up to limit messages, oldest first. It is what the AI
	// context builder will read.
	Recent(ctx context.Context, conversationID string, limit int) ([]conversation.Message, error)
}

// ProcessedEvents coordinates provider deliveries across service instances.
//
// Every provider this system targets retries deliveries, and a retry that is
// handled again produces a second reply and, later, a second appointment.
type ProcessedEvents interface {
	// Claim atomically acquires a short processing lease. It returns false when
	// another request owns the delivery or it has already been completed.
	Claim(ctx context.Context, key, claimID string, at time.Time) (bool, error)

	// Complete turns claimID's lease into a completed-delivery record.
	Complete(ctx context.Context, key, claimID string, at time.Time) error

	// Release gives a failed attempt back so a provider retry can run now.
	Release(ctx context.Context, key, claimID string) error
}

// Deps are the collaborators a Service needs.
//
// They are gathered into a struct rather than passed positionally because
// there are enough of them that call sites would otherwise be a row of
// same-typed arguments that are easy to transpose.
type Deps struct {
	Senders       map[messaging.Provider]Sender
	Customers     CustomerRepository
	Conversations ConversationRepository
	Messages      MessageRepository
	Processed     ProcessedEvents
	Logger        *slog.Logger

	// AI interprets the conversation. When it is absent the assistant falls
	// back to a fixed reply rather than failing, so the channels keep working
	// without a provider configured.
	AI ai.Provider

	// Scheduling is the calendar. Without it the assistant can talk but cannot
	// answer anything about services or availability.
	Scheduling Scheduling

	// Bookings records the appointments this system has made.
	Bookings BookingRepository

	// Reminders plans delayed notifications after a confirmed create or move.
	Reminders ReminderPlanner

	Business Business

	// Now supplies the current time. It is injected so tests can assert on
	// stored timestamps rather than tolerate whatever the clock said.
	Now func() time.Time
}

// Service handles one inbound message at a time.
type Service struct {
	senders       map[messaging.Provider]Sender
	customers     CustomerRepository
	conversations ConversationRepository
	messages      MessageRepository
	processed     ProcessedEvents
	logger        *slog.Logger
	now           func() time.Time
	ai            ai.Provider
	tools         *toolset
	business      Business
}

// NewService returns a Service, or reports which collaborator is missing.
func NewService(deps Deps) (*Service, error) {
	switch {
	case deps.Customers == nil:
		return nil, errors.New("assistant: customer repository is required")
	case deps.Conversations == nil:
		return nil, errors.New("assistant: conversation repository is required")
	case deps.Messages == nil:
		return nil, errors.New("assistant: message repository is required")
	case deps.Processed == nil:
		return nil, errors.New("assistant: processed event store is required")
	case deps.Logger == nil:
		return nil, errors.New("assistant: logger is required")
	}

	now := deps.Now
	if now == nil {
		now = time.Now
	}

	senders := deps.Senders
	if senders == nil {
		senders = map[messaging.Provider]Sender{}
	}

	business := deps.Business
	if business.Location == nil {
		business.Location = time.UTC
	}

	return &Service{
		senders:       senders,
		customers:     deps.Customers,
		conversations: deps.Conversations,
		messages:      deps.Messages,
		processed:     deps.Processed,
		logger:        deps.Logger,
		now:           now,
		ai:            deps.AI,
		business:      business,
		tools: &toolset{
			scheduling: deps.Scheduling,
			bookings:   deps.Bookings,
			reminders:  deps.Reminders,
			now:        now,
			location:   business.Location,
			logger:     deps.Logger,
		},
	}, nil
}

// maxToolRounds bounds the tool-calling loop.
//
// Without a limit a model that keeps asking for the same lookup would spend
// money and the customer's patience indefinitely.
//
// Six is sized for the longest legitimate exchange: a customer who states
// everything at once needs the catalogue, the specialists, the free times, a
// prepared booking and a confirmation before there is anything to say. Most
// messages need one round or none.
const maxToolRounds = 6

// recentMessageLimit bounds how much transcript is read back. The AI context
// builder will need a window, not the whole history.
const recentMessageLimit = 20

// deliveryStateTimeout bounds the cleanup that records or releases a claim
// after the webhook request itself has been cancelled.
const deliveryStateTimeout = 5 * time.Second

// Handle processes one normalised message.
//
// The order of the steps is deliberate. The delivery first acquires a short
// lease, which excludes a concurrent copy without declaring unfinished work
// complete. A failed attempt releases that lease; a successful one turns it
// into a longer completed record. If the process crashes, the lease expires and
// a later provider retry can recover the message.
func (s *Service) Handle(ctx context.Context, msg messaging.Envelope) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	now := s.now()
	claimID := id.New()
	claimed, err := s.processed.Claim(ctx, msg.DedupeKey(), claimID, now)
	if err != nil {
		return fmt.Errorf("claim the delivery: %w", err)
	}
	if !claimed {
		s.logger.InfoContext(ctx, "dropped an in-flight or completed delivery",
			"dedupe_key", msg.DedupeKey())
		return nil
	}

	// Errors before delivery leave no externally visible result, so release the
	// lease and let the provider retry. After a reply is sent the lease is kept
	// even if recording completion fails, avoiding an immediate second reply.
	releaseClaim := true
	defer func() {
		if releaseClaim {
			s.releaseDelivery(ctx, msg.DedupeKey(), claimID)
		}
	}()

	cust, err := s.identify(ctx, msg, now)
	if err != nil {
		return err
	}

	conv, err := s.openConversation(ctx, msg, cust, now)
	if err != nil {
		return err
	}

	if err := s.messages.Append(ctx, conversation.Message{
		ID:                id.New(),
		ConversationID:    conv.ID,
		Direction:         conversation.DirectionInbound,
		ContentType:       msg.Content.Type,
		Text:              msg.Content.Text,
		ExternalMessageID: msg.ExternalMessageID,
		CreatedAt:         now,
	}); err != nil {
		return fmt.Errorf("record the incoming message: %w", err)
	}

	s.logger.InfoContext(ctx, "handling a customer message",
		"provider", string(msg.Provider),
		"dedupe_key", msg.DedupeKey(),
		"customer_id", cust.ID,
		"conversation_id", conv.ID,
		"conversation_state", string(conv.State),
		"content_type", string(msg.Content.Type),
		// The message body is not logged. It is customer content, and the
		// system has no reason to keep a second copy of it in the logs.
		"content_length", len(msg.Content.Text),
	)

	// A colleague handling the conversation must not be talked over, and a
	// customer waiting for a person must not be answered by the bot again.
	if !conv.AssistantMayReply() {
		s.logger.InfoContext(ctx, "left the message for a colleague",
			"conversation_id", conv.ID, "conversation_state", string(conv.State))
		releaseClaim = false
		s.completeDelivery(ctx, msg.DedupeKey(), claimID, now)
		return nil
	}

	sender, ok := s.senders[msg.Provider]
	if !ok {
		return fmt.Errorf("no sender configured for %s", msg.Provider)
	}

	history, err := s.messages.Recent(ctx, conv.ID, recentMessageLimit)
	if err != nil {
		return fmt.Errorf("read the conversation history: %w", err)
	}

	text, err := s.reply(ctx, &conv, cust, msg, history)
	if err != nil {
		return err
	}

	// A tool may have handed the conversation to a colleague, and that state
	// change has to survive whatever happens next.
	if err := s.conversations.Save(ctx, conv); err != nil {
		return fmt.Errorf("save the conversation: %w", err)
	}

	reply := msg.Reply(text)
	if err := sender.Send(ctx, reply); err != nil {
		return fmt.Errorf("send the reply: %w", err)
	}

	// The customer already has the reply, so a failure to record it must not
	// fail the delivery: that would prompt a retry and a second reply.
	if err := s.messages.Append(ctx, conversation.Message{
		ID:             id.New(),
		ConversationID: conv.ID,
		Direction:      conversation.DirectionOutbound,
		ContentType:    messaging.ContentTypeText,
		Text:           reply.Text,
		CreatedAt:      s.now(),
	}); err != nil {
		s.logger.ErrorContext(ctx, "sent a reply but could not record it",
			"error", err, "conversation_id", conv.ID)
	}

	releaseClaim = false
	s.completeDelivery(ctx, msg.DedupeKey(), claimID, now)
	return nil
}

// History returns the recent transcript of a conversation, oldest first.
func (s *Service) History(ctx context.Context, conversationID string) ([]conversation.Message, error) {
	return s.messages.Recent(ctx, conversationID, recentMessageLimit)
}

func (s *Service) identify(ctx context.Context, msg messaging.Envelope, now time.Time) (customer.Customer, error) {
	customerID := id.New()

	cust, err := s.customers.FindOrCreateByChannelIdentity(ctx,
		customer.ChannelIdentity{
			ID:             id.New(),
			CustomerID:     customerID,
			Provider:       msg.Provider,
			ExternalUserID: msg.ExternalUserID,
			DisplayName:    msg.Sender.DisplayName,
			Language:       msg.Sender.Language,
			CreatedAt:      now,
		},
		customer.Customer{
			ID:        customerID,
			Name:      msg.Sender.DisplayName,
			CreatedAt: now,
			UpdatedAt: now,
		},
	)
	if err != nil {
		return customer.Customer{}, fmt.Errorf("identify the customer: %w", err)
	}
	return cust, nil
}

func (s *Service) openConversation(
	ctx context.Context,
	msg messaging.Envelope,
	cust customer.Customer,
	now time.Time,
) (conversation.Conversation, error) {
	conv, err := s.conversations.FindOrOpen(ctx, conversation.Conversation{
		ID:               id.New(),
		CustomerID:       cust.ID,
		Provider:         msg.Provider,
		ExternalThreadID: msg.ExternalThreadID,
		State:            conversation.StateAssistantActive,
		CreatedAt:        now,
		UpdatedAt:        now,
		LastMessageAt:    now,
	})
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("open the conversation: %w", err)
	}

	// A message arriving on a finished conversation starts it again rather
	// than being dropped.
	if conv.State == conversation.StateClosed {
		if err := conv.TransitionTo(conversation.StateAssistantActive, now); err != nil {
			return conversation.Conversation{}, fmt.Errorf("reopen the conversation: %w", err)
		}
	}

	conv.LastMessageAt = now
	conv.UpdatedAt = now

	if err := s.conversations.Save(ctx, conv); err != nil {
		return conversation.Conversation{}, fmt.Errorf("save the conversation: %w", err)
	}
	return conv, nil
}

// completeDelivery records that a delivery has been handled. A failure is logged
// rather than returned: the reply is already with the customer, and reporting
// an error would only cause the provider to send the message again.
func (s *Service) completeDelivery(ctx context.Context, key, claimID string, at time.Time) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryStateTimeout)
	defer cancel()

	if err := s.processed.Complete(cleanupCtx, key, claimID, at); err != nil {
		s.logger.ErrorContext(ctx, "handled a message but could not record it as processed",
			"error", err, "dedupe_key", key)
	}
}

// releaseDelivery makes a failed attempt immediately retryable. If the request
// context has already expired the repository's lease remains the crash-safe
// fallback and makes it retryable later.
func (s *Service) releaseDelivery(ctx context.Context, key, claimID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryStateTimeout)
	defer cancel()

	if err := s.processed.Release(cleanupCtx, key, claimID); err != nil {
		s.logger.ErrorContext(ctx, "could not release a failed delivery claim",
			"error", err, "dedupe_key", key)
	}
}

// reply produces what to say back, using the model when one is configured.
//
// The loop is the whole mechanism: the model asks for named tools, this code
// runs them, and the results go back for the model to phrase. The model never
// reaches the scheduling system, never sees a credential and never decides what
// is true; it decides only how to say what the tools returned.
func (s *Service) reply(
	ctx context.Context,
	conv *conversation.Conversation,
	cust customer.Customer,
	msg messaging.Envelope,
	history []conversation.Message,
) (string, error) {
	if s.ai == nil {
		return s.compose(msg, cust), nil
	}

	sess := &session{
		conv:              conv,
		customer:          cust,
		incomingMessageID: msg.ExternalMessageID,
	}

	req := ai.Request{
		Instructions: s.instructions(cust),
		Messages:     toAIMessages(history),
		Tools:        s.tools.definitions(),
	}

	for round := 1; round <= maxToolRounds; round++ {
		resp, err := s.ai.Complete(ctx, req)
		if err != nil {
			// A model that cannot be reached must not silence the assistant.
			// The customer gets an honest answer and a colleague picks it up.
			s.logger.ErrorContext(ctx, "the ai provider failed",
				"error", err, "conversation_id", conv.ID, "model", s.ai.Model())
			return s.escalate(ctx, conv)
		}

		s.logger.InfoContext(ctx, "completed an ai turn",
			"conversation_id", conv.ID,
			"model", s.ai.Model(),
			"round", round,
			"input_tokens", resp.Usage.InputTokens,
			"output_tokens", resp.Usage.OutputTokens,
			"tool_calls", len(resp.ToolCalls),
		)

		if !resp.WantsTools() {
			if resp.Text == "" {
				s.logger.WarnContext(ctx, "the model returned nothing to say",
					"conversation_id", conv.ID)
				return s.escalate(ctx, conv)
			}
			return resp.Text, nil
		}

		turn := ai.Turn{Calls: resp.ToolCalls}
		for _, tc := range resp.ToolCalls {
			s.logger.InfoContext(ctx, "running a tool for the assistant",
				"conversation_id", conv.ID, "tool", tc.Name)
			turn.Results = append(turn.Results, s.tools.execute(ctx, sess, tc))
		}
		req.Turns = append(req.Turns, turn)
	}

	// Out of rounds. Something is wrong with the conversation rather than with
	// the customer, so a colleague takes it rather than the loop continuing.
	s.logger.WarnContext(ctx, "gave up after too many tool rounds",
		"conversation_id", conv.ID, "rounds", maxToolRounds)
	return s.escalate(ctx, conv)
}

// escalate hands the conversation to a colleague and returns what to say.
func (s *Service) escalate(ctx context.Context, conv *conversation.Conversation) (string, error) {
	if err := conv.TransitionTo(conversation.StateHumanRequested, s.now()); err != nil {
		s.logger.ErrorContext(ctx, "could not hand the conversation over",
			"error", err, "conversation_id", conv.ID)
	}
	return "Sorry, I cannot check that for you right now. A colleague will get back to you shortly.", nil
}

// compose is the reply used when no model is configured.
//
// It is deliberately honest about what the assistant can and cannot do, rather
// than pretending to take a booking it has no way to make.
func (s *Service) compose(msg messaging.Envelope, cust customer.Customer) string {
	if msg.Content.Type == messaging.ContentTypeUnsupported {
		return fmt.Sprintf(
			"Thanks%s. I can only read text messages at the moment, so I could not open your %s. Could you describe what you need in a message?",
			greetingName(cust.Name), msg.Content.Description,
		)
	}

	return fmt.Sprintf(
		"Thanks%s, I have your message. A colleague will follow up with you shortly.",
		greetingName(cust.Name),
	)
}

func greetingName(name string) string {
	if name == "" {
		return ""
	}
	return " " + name
}
