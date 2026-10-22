package assistant

import (
	"context"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

// externalReplyRepository records transcript and takeover atomically, including
// replay protection. It deliberately does not wait for the assistant's lease:
// an already-running model turn must see that staff have started answering.
type externalReplyRepository interface {
	RecordExternalReplyObserved(context.Context, conversation.Conversation, conversation.Message, time.Time, time.Time) (conversation.Conversation, bool, error)
}

func (s *Service) RecordExternalReply(ctx context.Context, reply messaging.ExternalReply) error {
	if err := reply.Validate(); err != nil {
		return err
	}
	repository, ok := s.conversations.(externalReplyRepository)
	if !ok {
		return fmt.Errorf("assistant: external staff reply persistence is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	now := s.now()
	cust, err := s.identify(ctx, messaging.Envelope{
		Provider: reply.Provider, ExternalUserID: reply.ExternalThreadID,
	}, now)
	if err != nil {
		return err
	}
	conv, observed, err := repository.RecordExternalReplyObserved(ctx, conversation.Conversation{
		ID: id.New(), CustomerID: cust.ID, Provider: reply.Provider,
		ExternalThreadID: reply.ExternalThreadID, State: conversation.StateAssistantActive,
		CreatedAt: now, UpdatedAt: now, LastMessageAt: now,
	}, conversation.Message{
		ID: id.New(), Direction: conversation.DirectionOutbound,
		ContentType: reply.Content.Type, Text: reply.Content.Text,
		ExternalMessageID: reply.ExternalMessageID, CreatedAt: reply.SentAt,
	}, reply.SentAt, now)
	if err != nil {
		return err
	}
	if observed {
		history, _ := s.History(ctx, conv.ID)
		if len(history) > 6 {
			history = history[len(history)-6:]
		}
		s.notifyStaff(ctx, HandoffNotice{ConversationID: conv.ID, Reason: ReasonStaffReplied, Detail: "An administrator replied in the client chat. Assistant paused; use Resume to return it now, or it resumes on the next client message after 2 hours without an administrator reply.", Provider: reply.Provider, Customer: cust, ExternalUserID: reply.ExternalThreadID, Recent: history, RequestedAt: now})
	}
	return nil
}

func (s *Service) checkExternalReply(ctx context.Context, conv conversation.Conversation) error {
	if conv.Provider == "" {
		return nil
	} // Internal sessions without a channel cannot receive echoes.
	current, err := s.conversations.FindByID(ctx, conv.ID)
	if err != nil {
		return fmt.Errorf("check whether staff have replied: %w", err)
	}
	if current.ExternalReplyRevision != conv.ExternalReplyRevision {
		return conversation.ErrExternalReplyConflict
	}
	return nil
}
