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
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
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

	// UpdateContact records the name and phone number a customer has given.
	//
	// Messaging providers do not hand out phone numbers, so this is usually the
	// only way the business can call somebody back. It is written as soon as the
	// customer says it, so that a booking which then fails still leaves a way to
	// reach them.
	UpdateContact(ctx context.Context, customerID, name, phone string) error
}

// ConversationRepository stores conversations.
type ConversationRepository interface {
	// FindOrOpen returns the existing conversation for the channel thread
	// candidate names, or stores and returns candidate if there is none.
	FindOrOpen(ctx context.Context, candidate conversation.Conversation) (conversation.Conversation, error)

	// FindByID returns one conversation, reporting conversation.ErrNotFound
	// when there is none. It is how a colleague acting on a notification
	// reaches the conversation it names.
	FindByID(ctx context.Context, conversationID string) (conversation.Conversation, error)

	Save(ctx context.Context, conv conversation.Conversation) error
}

// MessageRepository stores the transcript.
type MessageRepository interface {
	// Append stores msg idempotently. A provider retry can reach this point
	// after an earlier attempt already recorded the inbound message, and that
	// must not put the same customer sentence into the model context twice.
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

// ConversationTurns records which customer message is currently the newest
// for a channel thread.
//
// Messages from one customer are still processed serially, but customers often
// send a thought as two or three short messages. When a newer part arrives
// while the model is answering an earlier part, the earlier answer is obsolete:
// the newest turn should answer all of the accumulated text once.
type ConversationTurns interface {
	RegisterTurn(ctx context.Context, conversationKey, eventID string, at time.Time) error
	IsLatestTurn(ctx context.Context, conversationKey, eventID string) (bool, error)
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
	Turns         ConversationTurns
	Logger        *slog.Logger

	// AI interprets the conversation. When it is absent the assistant falls
	// back to a fixed reply rather than failing, so the channels keep working
	// without a provider configured.
	AI     ai.Provider
	Speech ai.Transcriber

	// Scheduling is the calendar. Without it the assistant can talk but cannot
	// answer anything about services or availability.
	Scheduling Scheduling

	// Bookings records the appointments this system has made.
	Bookings BookingRepository

	// Staff is told when a conversation needs a person. Without it a handover
	// changes a stored state and nobody ever learns the customer is waiting.
	Staff StaffNotifier

	// Reminders plans delayed notifications after a confirmed create or move.
	Reminders ReminderPlanner

	// AppointmentMessages renders the deterministic success message after the
	// calendar confirms a booking. The zero value is safe and simply omits
	// optional business details.
	AppointmentMessages appointmentmessage.Renderer

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
	turns         ConversationTurns
	activeTurns   *activeTurnRegistry
	logger        *slog.Logger
	now           func() time.Time
	ai            ai.Provider
	speech        ai.Transcriber
	tools         *toolset
	business      Business
	staff         StaffNotifier
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
	case deps.Turns == nil:
		return nil, errors.New("assistant: conversation turn store is required")
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
		turns:         deps.Turns,
		activeTurns:   newActiveTurnRegistry(),
		logger:        deps.Logger,
		now:           now,
		ai:            deps.AI,
		speech:        deps.Speech,
		business:      business,
		staff:         deps.Staff,
		tools: &toolset{
			scheduling: deps.Scheduling,
			bookings:   deps.Bookings,
			customers:  deps.Customers,
			reminders:  deps.Reminders,
			messages:   deps.AppointmentMessages,
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
func (s *Service) Handle(ctx context.Context, msg messaging.Envelope) (resultErr error) {
	if err := msg.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

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

	turnKey := conversation.Key(msg.Provider, msg.ExternalThreadID)
	if err := s.turns.RegisterTurn(ctx, turnKey, msg.ExternalMessageID, now); err != nil {
		return fmt.Errorf("register the customer turn: %w", err)
	}
	// A newer fragment makes any model answer currently being composed for the
	// same chat obsolete. This cancels it immediately when both requests land on
	// this instance; the durable latest-turn check below covers other instances.
	s.activeTurns.Supersede(turnKey)

	// Errors before delivery leave no externally visible result, so release the
	// lease and let the provider retry. After a reply is sent the lease is kept
	// even if recording completion fails, avoiding an immediate second reply.
	releaseClaim := true
	defer func() {
		if errors.Is(resultErr, conversation.ErrExternalReplyConflict) {
			// The customer has already heard from staff. A stale model answer
			// is obsolete, and retrying this turn must not produce another one.
			s.logger.InfoContext(ctx, "suppressed a reply after staff answered", "dedupe_key", msg.DedupeKey())
			releaseClaim = false
			s.completeDelivery(ctx, msg.DedupeKey(), claimID, s.now())
			resultErr = nil
		}
		if releaseClaim {
			s.releaseDelivery(ctx, msg.DedupeKey(), claimID)
		}
	}()

	unlock, err := s.lockConversation(ctx, msg, claimID)
	if err != nil {
		return err
	}
	defer unlock()
	// Record the actual processing time after waiting for the preceding turn.
	now = s.now()

	cust, err := s.identify(ctx, msg, now)
	if err != nil {
		return err
	}
	// Phone messages often arrive immediately before another short answer.
	// Persist an unambiguous, standalone number before any model work so a
	// coalesced follow-up does not make the assistant ask for it again.
	s.rememberInboundPhone(ctx, &cust, msg)

	conv, err := s.openConversation(ctx, msg, cust, now)
	if err != nil {
		return err
	}

	// A callback must answer the current prompt. In particular an old "yes"
	// cannot confirm a booking prepared after that button was sent.
	retryingChoice := msg.ChoiceMessageID == conv.PendingChoiceMessageID && msg.ExternalMessageID == conv.PendingChoiceEventID
	if conv.AssistantMayReply() && msg.ChoiceMessageID != "" && msg.ChoiceMessageID != conv.LastChoiceMessageID && !retryingChoice {
		sender, ok := s.senders[msg.Provider]
		if !ok {
			return fmt.Errorf("no sender configured for %s", msg.Provider)
		}
		history, err := s.messages.Recent(ctx, conv.ID, recentMessageLimit)
		if err != nil {
			return err
		}
		if err := sender.Send(ctx, msg.Reply(expiredChoice(conversationLanguage(history, msg.Sender.Language)))); err != nil {
			return err
		}
		releaseClaim = false
		s.completeDelivery(ctx, msg.DedupeKey(), claimID, now)
		return nil
	}

	audioFailed := false
	if msg.Content.Type == messaging.ContentTypeAudio && (conv.AssistantMayReply() || conv.WaitingForHumanLongerThan(handoffTimeout, now)) {
		transcribed, err := s.transcribeInput(ctx, msg)
		if err != nil {
			s.logger.WarnContext(ctx, "could not transcribe customer audio", "error", err, "conversation_id", conv.ID)
			// A caption is customer-authored text. If the voice note is unreadable,
			// answer that text now instead of making the customer repeat it.
			if caption := strings.TrimSpace(msg.Content.Text); caption != "" {
				msg.Content = messaging.Content{Type: messaging.ContentTypeText, Text: caption}
			} else {
				audioFailed = true
				msg.Content = messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "voice message"}
			}
		} else {
			msg = transcribed
		}
	}

	// A numbered answer is resolved only against the options in the last reply
	// that was successfully delivered. Keep the customer's literal "2" in the
	// transcript, while handing the canonical category or service name to the
	// conversation logic below.
	resolvedChoice, selectedPresentedChoice := conv.ResolvePresentedChoice(msg.Content.Text)
	originalText := msg.Content.Text
	if selectedPresentedChoice {
		msg.Content.Text = resolvedChoice
	}

	if err := s.messages.Append(ctx, conversation.Message{
		ID:                id.New(),
		ConversationID:    conv.ID,
		Direction:         conversation.DirectionInbound,
		ContentType:       msg.Content.Type,
		Text:              originalText,
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

	// A colleague was asked for and never arrived. Resuming is better than
	// leaving the customer talking to nobody indefinitely.
	if conv.WaitingForHumanLongerThan(handoffTimeout, now) {
		if err := conv.TransitionTo(conversation.StateAssistantActive, now); err != nil {
			s.logger.ErrorContext(ctx, "could not resume a conversation nobody picked up",
				"error", err, "conversation_id", conv.ID)
		} else {
			s.logger.InfoContext(ctx, "resumed a conversation nobody picked up",
				"conversation_id", conv.ID,
				"waited", now.Sub(conv.HandoffAt).String(),
			)
		}
	}

	// A colleague handling the conversation must not be talked over, and a
	// customer waiting for a person must not be answered by the bot again.
	if !conv.AssistantMayReply() {
		// Consent withdrawal remains effective during handover. Do not send an
		// automated reply over the colleague or interpret STOP as cancellation.
		if conv.Provider == messaging.ProviderWhatsApp && stopsReminders(msg.Content.Text) {
			conv.ReminderOptIn = false
			if err := s.conversations.Save(ctx, conv); err != nil {
				return err
			}
		}
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

	turnCtx, finishTurn := s.activeTurns.Activate(ctx, turnKey, msg.ExternalMessageID)
	defer finishTurn()

	completeSuperseded := func(stage string) error {
		// Tools may already have prepared a draft before the follow-up arrived.
		// Keep that truthful state when possible, but never retry an obsolete
		// customer turn solely because this best-effort save failed.
		if saveErr := s.conversations.Save(ctx, conv); saveErr != nil {
			s.logger.WarnContext(ctx, "could not save an obsolete assistant turn",
				"error", saveErr, "conversation_id", conv.ID, "stage", stage)
		}
		s.logger.InfoContext(ctx, "coalesced an obsolete customer turn into a newer message",
			"conversation_id", conv.ID, "stage", stage)
		releaseClaim = false
		s.completeDelivery(ctx, msg.DedupeKey(), claimID, s.now())
		return nil
	}

	isLatest, err := s.turns.IsLatestTurn(turnCtx, turnKey, msg.ExternalMessageID)
	if err != nil {
		if errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			return completeSuperseded("before_history")
		}
		return fmt.Errorf("check the customer turn before reading history: %w", err)
	}
	if !isLatest {
		return completeSuperseded("before_history")
	}

	history, err := s.messages.Recent(turnCtx, conv.ID, recentMessageLimit)
	if err != nil {
		if errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			return completeSuperseded("reading_history")
		}
		return fmt.Errorf("read the conversation history: %w", err)
	}
	if selectedPresentedChoice {
		// Recent returns a detached slice. Replacing only this request's inbound
		// text gives the model the meaning of the number without falsifying the
		// stored customer transcript.
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Direction == conversation.DirectionInbound &&
				history[i].ExternalMessageID == msg.ExternalMessageID {
				history[i].Text = resolvedChoice
				break
			}
		}
	}

	sess := &session{
		conv:              &conv,
		customer:          cust,
		incomingMessageID: msg.ExternalMessageID,

		// Settled once, from what has been said so far, and used for the few
		// phrases this system writes itself rather than asking the model for.
		language: conversationLanguage(history, msg.Sender.Language),
	}
	conv.PendingChoiceMessageID, conv.PendingChoiceEventID = "", ""
	if msg.ChoiceMessageID != "" {
		conv.PendingChoiceMessageID, conv.PendingChoiceEventID = msg.ChoiceMessageID, msg.ExternalMessageID
	}
	s.retireChoices(ctx, sender, &conv)

	// Bound the whole model/tool loop, leaving time to deliver a useful retry
	// response even when a provider is slow. Typing feedback runs independently.
	replyCtx, cancelReply := context.WithTimeout(turnCtx, 45*time.Second)
	var text string
	if audioFailed {
		text = audioRetry(sess.language)
	} else {
		text, err = s.reply(replyCtx, sess, msg, history, selectedPresentedChoice)
	}
	cancelReply()
	if err != nil {
		if errors.Is(err, errTurnSuperseded) || errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			return completeSuperseded("composing_reply")
		}
		return err
	}

	// A tool may have handed the conversation to a colleague, and that state
	// change has to survive whatever happens next.
	if err := s.conversations.Save(ctx, conv); err != nil {
		return fmt.Errorf("save the conversation: %w", err)
	}

	// Told after the conversation is saved, so a colleague acting on the
	// notification cannot arrive before the state they are told about exists.
	if sess.handoffReason != "" {
		s.notifyStaff(ctx, HandoffNotice{
			ConversationID: conv.ID,
			Reason:         sess.handoffReason,
			Detail:         sess.handoffDetail,
			Provider:       msg.Provider,
			// Read from the session, not the value loaded at the start: a tool
			// may have recorded a phone number during this exchange, and that is
			// exactly the detail the colleague needs.
			Customer:       sess.customer,
			Handle:         msg.Sender.Username,
			ExternalUserID: msg.ExternalUserID,
			Recent:         history,
			Draft:          conv.Draft,
			RequestedAt:    now,
		})
	}

	// A newer message may have arrived on another Cloud Run instance while the
	// model or calendar was working. Check the shared cursor immediately before
	// the externally visible send, so only the answer to the newest fragment is
	// delivered.
	isLatest, err = s.turns.IsLatestTurn(turnCtx, turnKey, msg.ExternalMessageID)
	if err != nil {
		if errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			return completeSuperseded("before_send")
		}
		return fmt.Errorf("check the customer turn before sending: %w", err)
	}
	if !isLatest {
		return completeSuperseded("before_send")
	}

	// Whatever the exchange ended up offering travels with the reply. Exact
	// numbered copies of native actions are noise; informative catalogue lines
	// carrying a price or duration remain untouched.
	choices := sess.buttons(text)
	if sess.offering != offerNavigation {
		text = withoutRedundantChoiceLines(text, choices)
	}
	reply := msg.Reply(text).WithChoices(choices).WithLinks(sess.finalLinks)
	if len(reply.Choices) > 0 {
		reply.ChoiceToken = id.New()
	}
	presentedChoices := sess.presentedChoices(text)
	if err := s.checkExternalReply(turnCtx, conv); err != nil {
		if errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			return completeSuperseded("checking_staff_reply")
		}
		return err
	}
	var sentID string
	if tracked, ok := sender.(choiceSender); ok {
		sentID, err = tracked.SendTracked(turnCtx, reply)
	} else {
		err = sender.Send(turnCtx, reply)
	}
	if err != nil {
		if errors.Is(context.Cause(turnCtx), errTurnSuperseded) {
			// Delivery may have reached the provider before cancellation became
			// observable. The newer turn will answer the customer either way, and
			// retrying this old one risks a duplicate or out-of-order response.
			return completeSuperseded("sending_reply")
		}
		return fmt.Errorf("send the reply: %w", err)
	}
	tracked, tracksChoices := sender.(choiceSender)
	if tracksChoices {
		conv.LastChoiceMessageID = ""
		conv.PendingChoiceMessageID, conv.PendingChoiceEventID = "", ""
		if len(reply.Choices) > 0 {
			conv.LastChoiceMessageID = sentID
		}
	}
	conv.PresentedChoices = presentedChoices
	if !tracksChoices {
		conv.LastChoiceMessageID = reply.ChoiceToken
		conv.PendingChoiceMessageID, conv.PendingChoiceEventID = "", ""
	}
	if err := s.conversations.Save(ctx, conv); err != nil {
		s.logger.ErrorContext(ctx, "sent a reply but could not store its current choices", "error", err, "conversation_id", conv.ID)
		if tracksChoices {
			// A button we cannot identify later is worse than no button: it looks
			// usable and then gets rejected as stale. Remove it from the delivered
			// message immediately; the same choices remain readable in its text.
			if len(reply.Choices) > 0 && sentID != "" {
				s.retireUntrackedChoices(ctx, tracked, conv.ExternalThreadID, sentID, conv.ID)
			}
		}
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

func withoutRedundantChoiceLines(text string, choices []messaging.Choice) string {
	if len(choices) == 0 {
		return text
	}
	labels := make(map[string]bool, len(choices))
	for _, choice := range choices {
		labels[strings.ToLower(strings.TrimSpace(choice.Label))] = true
	}
	kept := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		var candidate string
		if _, ok := numberedLine(trimmed); ok {
			position := strings.IndexAny(trimmed, ".)")
			candidate = strings.TrimSpace(trimmed[position+1:])
		} else {
			candidate = strings.TrimSpace(strings.TrimLeft(trimmed, "•-–—"))
		}
		if labels[strings.ToLower(candidate)] {
			continue
		}
		switch strings.ToLower(trimmed) {
		case "tap a button or send the number.",
			"tap a button or send its number. you can also write to us.",
			"нажмите кнопку или отправьте номер.",
			"нажмите кнопку или отправьте её номер. можно также написать нам.",
			"սեղմեք կոճակը կամ ուղարկեք համարը։ Կարող եք նաև գրել մեզ։",
			"սեղմեք կոճակը կամ ուղարկեք համարը։":
			continue
		}
		kept = append(kept, line)
	}
	cleaned := strings.TrimSpace(strings.Join(kept, "\n"))
	if cleaned == "" {
		return strings.TrimSpace(text)
	}
	return cleaned
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
	if cust.Name == "" && strings.TrimSpace(msg.Sender.DisplayName) != "" {
		name := strings.TrimSpace(msg.Sender.DisplayName)
		if updateErr := s.customers.UpdateContact(ctx, cust.ID, name, cust.Phone); updateErr != nil {
			s.logger.WarnContext(ctx, "could not remember the provider-visible customer name", "error", updateErr, "customer_id", cust.ID)
		} else {
			cust.Name = name
		}
	}
	// A WhatsApp sender id is the customer's phone number. Unlike profile names
	// on other channels, this value is an address the customer has just used and
	// can safely prefill the booking contact instead of asking them to type it.
	if msg.Provider == messaging.ProviderWhatsApp && cust.Phone == "" {
		if phone, phoneErr := customer.NormalizePhone("+" + strings.TrimPrefix(msg.ExternalUserID, "+")); phoneErr == nil {
			if updateErr := s.customers.UpdateContact(ctx, cust.ID, cust.Name, phone); updateErr != nil {
				s.logger.WarnContext(ctx, "could not remember the WhatsApp contact number", "error", updateErr, "customer_id", cust.ID)
			} else {
				cust.Phone = phone
			}
		}
	}
	return cust, nil
}

func (s *Service) rememberInboundPhone(ctx context.Context, cust *customer.Customer, msg messaging.Envelope) {
	if cust == nil || msg.Content.Type != messaging.ContentTypeText {
		return
	}
	phone, err := customer.NormalizePhone(strings.TrimSpace(msg.Content.Text))
	if err != nil || phone == cust.Phone {
		return
	}
	if err := s.customers.UpdateContact(ctx, cust.ID, cust.Name, phone); err != nil {
		s.logger.WarnContext(ctx, "could not remember a phone number from the conversation", "error", err, "customer_id", cust.ID)
		return
	}
	cust.Phone = phone
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
	sess *session,
	msg messaging.Envelope,
	history []conversation.Message,
	selectedPresentedChoice bool,
) (string, error) {
	conv, cust := sess.conv, sess.customer

	// Some menu entries are answered here rather than by the model: they say
	// the same thing every time, and answering them from a table means they
	// arrive instantly and keep working when nothing else does.
	if text, ok := s.menuReply(ctx, sess, msg, selectedPresentedChoice); ok {
		return text, nil
	}

	// Confirmation is a state transition, not a language-understanding puzzle.
	// Handle an explicit yes in code so short Armenian transliterations such as
	// "ayo" cannot fall into a generic model response after a valid proposal.
	var seededTurns []ai.Turn
	if conv.Draft != nil && explicitlyConfirmsBooking(msg.Content.Text, sess.language) {
		call := ai.ToolCall{ID: "application-confirmation", Name: toolConfirmBooking, Arguments: []byte(`{}`)}
		result := s.tools.execute(ctx, sess, call)
		if sess.finalReply != "" {
			return sess.finalReply, nil
		}
		seededTurns = append(seededTurns, ai.Turn{Calls: []ai.ToolCall{call}, Results: []ai.ToolResult{result}})
	}

	if s.ai == nil {
		return s.compose(msg, sess.language), nil
	}

	req := ai.Request{
		Instructions:    s.instructions(cust, sess.language, msg.Sender.Language),
		Messages:        toAIMessages(history),
		Turns:           seededTurns,
		Tools:           s.tools.definitions(),
		StructuredReply: true,
	}

	for round := 1; round <= maxToolRounds; round++ {
		if errors.Is(context.Cause(ctx), errTurnSuperseded) {
			return "", errTurnSuperseded
		}
		resp, err := s.ai.Complete(ctx, req)
		if err != nil {
			if errors.Is(context.Cause(ctx), errTurnSuperseded) {
				return "", errTurnSuperseded
			}
			// A model that cannot be reached must not silence the assistant.
			// The customer gets an honest answer and a colleague picks it up.
			s.logger.ErrorContext(ctx, "the ai provider failed",
				"error", err, "conversation_id", conv.ID, "model", s.ai.Model())
			return s.apologise(ctx, sess)
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
				return s.apologise(ctx, sess)
			}
			sess.selectChoices(resp.Choices)
			return resp.Text, nil
		}
		if repeatedLookupWithoutProgress(resp.ToolCalls, req.Turns) {
			s.logger.WarnContext(ctx, "stopped repeated lookups that made no progress",
				"conversation_id", conv.ID, "round", round)
			return s.apologise(ctx, sess)
		}

		turn := ai.Turn{Calls: resp.ToolCalls}
		for _, tc := range resp.ToolCalls {
			if errors.Is(context.Cause(ctx), errTurnSuperseded) {
				return "", errTurnSuperseded
			}
			if err := s.checkExternalReply(ctx, *sess.conv); err != nil {
				return "", err
			}
			s.logger.InfoContext(ctx, "running a tool for the assistant",
				"conversation_id", conv.ID, "tool", tc.Name)
			turn.Results = append(turn.Results, s.tools.execute(ctx, sess, tc))
			// Once the calendar has accepted a booking, send the application's
			// factual confirmation immediately. A second model round would be
			// slower and could paraphrase away or invent practical visit details.
			if sess.finalReply != "" {
				return sess.finalReply, nil
			}
		}
		req.Turns = append(req.Turns, turn)
	}

	// Out of rounds. Something is wrong with the conversation rather than with
	// the customer, so a colleague takes it rather than the loop continuing.
	s.logger.WarnContext(ctx, "gave up after too many tool rounds",
		"conversation_id", conv.ID, "rounds", maxToolRounds)
	return s.apologise(ctx, sess)
}

func explicitlyConfirmsBooking(text string, lang language) bool {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(text), "!.,?։"))
	if strings.EqualFold(value, strings.ToLower(speak(lang).confirmBooking)) {
		return true
	}
	switch value {
	case "yes", "yes book it", "book it", "confirm", "confirmed",
		"да", "да запишите", "запишите", "подтверждаю", "да подтверждаю",
		"այո", "այո ամրագրեք", "հաստատում եմ", "ayo", "ayo amragreq", "ha", "ha amragreq":
		return true
	default:
		return false
	}
}

// repeatedLookupWithoutProgress catches a model asking for the same read-only
// lookup after receiving the same answer twice. A second attempt is useful if
// the calendar briefly failed; a third identical attempt only makes the
// customer wait. Booking and handoff tools are intentionally excluded because
// they change state and have their own idempotency and confirmation rules.
func repeatedLookupWithoutProgress(calls []ai.ToolCall, turns []ai.Turn) bool {
	if len(calls) == 0 || len(turns) < 2 {
		return false
	}
	previous, before := turns[len(turns)-1], turns[len(turns)-2]
	if len(calls) != len(previous.Calls) || len(calls) != len(before.Calls) ||
		len(previous.Results) != len(calls) || len(before.Results) != len(calls) {
		return false
	}
	for i, call := range calls {
		switch call.Name {
		case toolListCategories, toolListServices, toolListStaff,
			toolAvailableDates, toolAvailableSlots, toolListBookings:
		default:
			return false
		}
		if call.Name != previous.Calls[i].Name || call.Name != before.Calls[i].Name ||
			strings.TrimSpace(string(call.Arguments)) != strings.TrimSpace(string(previous.Calls[i].Arguments)) ||
			strings.TrimSpace(string(call.Arguments)) != strings.TrimSpace(string(before.Calls[i].Arguments)) ||
			previous.Results[i].Output != before.Results[i].Output {
			return false
		}
	}
	return true
}

// menuReply answers the menu entries that do not need a model.
//
// Opening the chat is the first thing anybody does and the one thing that must
// never be slow, wrong or missing, so its answer is written here and shipped
// with the code rather than asked for at the moment it is needed.
//
// Asking for a person is here for the opposite reason: not because the answer
// is always the same, but because the request admits of no interpretation. A
// customer who taps "Talk to a person" has said the one thing this system never
// needs a model to understand, and routing it through one only adds a way for
// it to be missed.
//
// Catalogue browsing reads live services without a model round trip. Free-text
// availability and booking requests continue through the guarded tool flow.
func (s *Service) menuReply(
	ctx context.Context,
	sess *session,
	msg messaging.Envelope,
	selectedPresentedChoice bool,
) (string, bool) {
	if text, ok := s.reminderConsent(ctx, sess, msg.Content.Text); ok {
		return text, true
	}
	if (menuAction(msg.Content.Text) == "start" || isGreeting(msg.Content.Text)) && s.tools != nil && s.tools.scheduling != nil {
		text, ok := s.catalogueReply(ctx, sess, "/services", false)
		if ok {
			intro := strings.SplitN(s.greeting(sess.language), "\n\n", 3)
			if len(intro) > 2 {
				intro = intro[:2]
			}
			return strings.Join(intro, "\n\n") + "\n\n" + text, true
		}
	}
	switch menuAction(msg.Content.Text) {
	case "start", "help":
		sess.offerFixed(offerMenu)
		return s.greeting(sess.language), true
	case "person":
		return s.handOver(ctx, sess)
	}
	return s.catalogueReply(ctx, sess, msg.Content.Text, selectedPresentedChoice)
}

// handOver answers a customer who asked for a person outright.
//
// The state change is what actually stops the assistant replying; the sentence
// is only what the customer reads. If the state will not change, nothing is
// promised: the request falls through to the model, which still has the handoff
// tool, rather than the customer being told somebody is coming when the record
// that would tell them says otherwise.
func (s *Service) handOver(ctx context.Context, sess *session) (string, bool) {
	if err := sess.conv.TransitionTo(conversation.StateHumanRequested, s.now()); err != nil {
		s.logger.ErrorContext(ctx, "could not hand over a conversation on request",
			"error", err,
			"conversation_id", sess.conv.ID,
			"conversation_state", string(sess.conv.State),
		)
		return "", false
	}

	sess.handoffReason = ReasonCustomerAsked
	sess.handoffDetail = "tapped the menu entry asking for a person"

	// A customer waiting for a person is waiting, not choosing.
	sess.offer()

	return speak(sess.language).handedOver, true
}

// apologise is the reply when the assistant itself failed: the model was
// unreachable, said nothing, or went round in circles.
//
// It deliberately does NOT hand the conversation to a colleague. A technical
// fault is not a reason to mute the assistant for the rest of this customer's
// life, and that is exactly what handing over would do: every later message
// would be silently swallowed. Leaving the state alone means the next message
// is attempted normally, which is usually all that is needed.
func (s *Service) apologise(ctx context.Context, sess *session) (string, error) {
	s.logger.WarnContext(ctx, "answering with an apology after an assistant failure",
		"conversation_id", sess.conv.ID, "conversation_state", string(sess.conv.State))

	// The one button worth showing when this system has just failed: somebody
	// who has not. Written here in the customer's language because the part of
	// the system that would normally do the writing is the part that failed.
	sess.offerFixed(offerHelp)

	return speak(sess.language).apology, nil
}

// compose is the reply used when no model is configured.
//
// It is deliberately honest about what the assistant can and cannot do, rather
// than pretending to take a booking it has no way to make.
//
// It says nothing the customer told it and names nothing the provider sent.
// Threading a name or an attachment type through a sentence is what forces a
// sentence to be built out of fragments, and a sentence built out of fragments
// only reads correctly in the language it was written in. Every other fixed
// reply in this system follows the customer's language; this one now does too,
// and the price is a greeting that does not use their name.
func (s *Service) compose(msg messaging.Envelope, lang language) string {
	p := speak(lang)
	if msg.Content.Type == messaging.ContentTypeUnsupported {
		return p.noModelUnsupported
	}
	return p.noModel
}
