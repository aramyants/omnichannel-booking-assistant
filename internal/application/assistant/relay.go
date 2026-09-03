package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

// StaffReply is a colleague answering a customer through the staff chat.
//
// It arrives as a reply to the notification that announced the conversation,
// which is what ties it to the right customer without anybody copying an
// identifier around.
type StaffReply struct {
	ConversationID string

	// AuthorName is the colleague writing. The customer is shown it, because
	// they asked for a person and pretending otherwise is a lie the business
	// has to keep up for the rest of the conversation.
	AuthorName string

	Text string
}

// Validate reports whether the reply can be delivered.
func (r StaffReply) Validate() error {
	switch {
	case r.ConversationID == "":
		return errors.New("staff reply: no conversation")
	case strings.TrimSpace(r.Text) == "":
		return errors.New("staff reply: no text")
	}
	return nil
}

// RelayStaffReply sends a colleague's message to the customer.
//
// The conversation moves to human_active, so the assistant stays out of the way
// while a person is talking. It goes back only when somebody hands it back.
func (s *Service) RelayStaffReply(ctx context.Context, reply StaffReply) error {
	if err := reply.Validate(); err != nil {
		return err
	}

	conv, err := s.conversations.FindByID(ctx, reply.ConversationID)
	if err != nil {
		return fmt.Errorf("find the conversation a colleague replied to: %w", err)
	}

	sender, ok := s.senders[conv.Provider]
	if !ok {
		return fmt.Errorf("no sender configured for %s", conv.Provider)
	}

	text := reply.Text
	if name := strings.TrimSpace(reply.AuthorName); name != "" {
		// Signed, so the customer knows a person has taken over rather than
		// wondering why the assistant suddenly sounds different.
		text = name + ": " + reply.Text
	}

	if err := sender.Send(ctx, messaging.Outgoing{
		Provider:         conv.Provider,
		ExternalThreadID: conv.ExternalThreadID,
		Text:             text,
	}); err != nil {
		return fmt.Errorf("send a colleague's reply: %w", err)
	}

	now := s.now()

	// Recorded so the transcript is what was actually said to this customer,
	// whoever said it. When the assistant resumes it reads this and knows.
	if err := s.messages.Append(ctx, conversation.Message{
		ID:             id.New(),
		ConversationID: conv.ID,
		Direction:      conversation.DirectionOutbound,
		ContentType:    messaging.ContentTypeText,
		Text:           text,
		CreatedAt:      now,
	}); err != nil {
		s.logger.ErrorContext(ctx, "relayed a colleague's reply but could not record it",
			"error", err, "conversation_id", conv.ID)
	}

	// A colleague who is answering has effectively taken the conversation,
	// whether or not they said so.
	if conv.State != conversation.StateHumanActive {
		if err := conv.TransitionTo(conversation.StateHumanActive, now); err != nil {
			s.logger.WarnContext(ctx, "could not mark a conversation as handled by a person",
				"error", err, "conversation_id", conv.ID, "state", string(conv.State))
		}
	}
	conv.UpdatedAt = now

	if err := s.conversations.Save(ctx, conv); err != nil {
		return fmt.Errorf("save the conversation after a colleague replied: %w", err)
	}

	s.logger.InfoContext(ctx, "relayed a colleague's reply to a customer",
		"conversation_id", conv.ID, "author", reply.AuthorName)

	return nil
}

// StaffCommand is an instruction a colleague typed in the staff chat.
type StaffCommand string

const (
	// CommandResume hands the conversation back to the assistant.
	CommandResume StaffCommand = "resume"

	// CommandTake marks the conversation as one a person is handling, which
	// stops the assistant answering while they work.
	CommandTake StaffCommand = "take"
)

// RunStaffCommand applies a colleague's instruction to a conversation and
// returns what to tell the staff chat.
func (s *Service) RunStaffCommand(
	ctx context.Context,
	command StaffCommand,
	conversationID string,
) (string, error) {
	conv, err := s.conversations.FindByID(ctx, conversationID)
	if err != nil {
		return "", fmt.Errorf("find the conversation: %w", err)
	}

	var next conversation.State
	switch command {
	case CommandResume:
		next = conversation.StateAssistantActive
	case CommandTake:
		next = conversation.StateHumanActive
	default:
		return "", fmt.Errorf("there is no command %q", command)
	}

	if err := conv.TransitionTo(next, s.now()); err != nil {
		return "", err
	}
	if err := s.conversations.Save(ctx, conv); err != nil {
		return "", fmt.Errorf("save the conversation: %w", err)
	}

	s.logger.InfoContext(ctx, "a colleague changed who is handling a conversation",
		"conversation_id", conv.ID, "command", string(command), "state", string(next))

	switch command {
	case CommandResume:
		return "The assistant is answering this customer again.", nil
	default:
		return "Noted, you are handling this one. The assistant will stay out of it.", nil
	}
}
