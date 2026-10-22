package firestore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

// RecordExternalReply atomically records manual activity and interrupts stale
// assistant turns, across instances. The deterministic document key keeps echo
// replays idempotent for as long as the transcript exists, including after resume.
func (s *Store) RecordExternalReply(ctx context.Context, candidate conversation.Conversation, msg conversation.Message, sentAt, receivedAt time.Time) error {
	_, _, err := s.RecordExternalReplyObserved(ctx, candidate, msg, sentAt, receivedAt)
	return err
}
func (s *Store) RecordExternalReplyObserved(ctx context.Context, candidate conversation.Conversation, msg conversation.Message, sentAt, receivedAt time.Time) (conversation.Conversation, bool, error) {
	ref := s.client.Collection(collectionConversations).Doc(candidate.Key())
	var result conversation.Conversation
	observed := false
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		observed = false
		conv := candidate
		snapshot, err := tx.Get(ref)
		if err == nil {
			var doc conversationDoc
			if err := snapshot.DataTo(&doc); err != nil {
				return err
			}
			conv = fromConversationDoc(doc)
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		result = conv
		messageRef := s.client.Collection(collectionMessages).Doc(conv.ID).
			Collection(collectionMessages).Doc(fmt.Sprintf("external-%x", sha256.Sum256([]byte(msg.ExternalMessageID))))
		if _, err := tx.Get(messageRef); err == nil {
			return nil
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		previousRevision := conv.ExternalReplyRevision
		conv.ObserveExternalReply(sentAt, receivedAt)
		observed = conv.ExternalReplyRevision != previousRevision
		result = conv
		if err := tx.Set(messageRef, messageDoc{
			ID: msg.ID, SortID: msg.ID, ConversationID: conv.ID,
			Direction: string(msg.Direction), ContentType: string(msg.ContentType),
			Text: msg.Text, ExternalMessageID: msg.ExternalMessageID, CreatedAt: msg.CreatedAt,
		}); err != nil {
			return err
		}
		return tx.Set(ref, toConversationDoc(conv))
	})
	if err != nil {
		return result, false, fmt.Errorf("firestore: record external staff reply: %w", err)
	}
	return result, observed, nil
}
