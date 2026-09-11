package memory

import (
	"context"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

// RecordExternalReply commits the echo and human ownership under one mutex.
func (s *Store) RecordExternalReply(_ context.Context, candidate conversation.Conversation, msg conversation.Message, sentAt, receivedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, exists := s.conversations[candidate.Key()]
	if !exists {
		conv = candidate
	}
	for _, previous := range s.messages[conv.ID] {
		if previous.Direction == conversation.DirectionOutbound && previous.ExternalMessageID == msg.ExternalMessageID {
			return nil
		}
	}
	msg.ConversationID = conv.ID
	s.messages[conv.ID] = append(s.messages[conv.ID], msg)
	conv.ObserveExternalReply(sentAt, receivedAt)
	s.conversations[conv.Key()] = conv
	s.conversationKeyByID[conv.ID] = conv.Key()
	return nil
}
