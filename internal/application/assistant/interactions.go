package assistant

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

var errTurnSuperseded = errors.New("assistant turn superseded by a newer customer message")

// activeTurnRegistry cancels slow work for an obsolete customer turn on this
// process. A pointer to the registry lives on Service so shallow service copies
// used by adapters and tests still coordinate with one another. The durable
// ConversationTurns store remains the authority across Cloud Run instances.
type activeTurnRegistry struct {
	mu      sync.Mutex
	current map[string]*activeTurn
}

type activeTurn struct {
	eventID string
	cancel  context.CancelCauseFunc
}

func newActiveTurnRegistry() *activeTurnRegistry {
	return &activeTurnRegistry{current: make(map[string]*activeTurn)}
}

// Supersede stops an answer currently being composed for conversationKey.
func (r *activeTurnRegistry) Supersede(conversationKey string) {
	r.mu.Lock()
	current := r.current[conversationKey]
	r.mu.Unlock()
	if current != nil {
		current.cancel(errTurnSuperseded)
	}
}

// Activate makes eventID the cancellable turn on this process. The caller must
// invoke the returned function; it only removes its own entry, so a late defer
// from an older request cannot delete a newer active turn.
func (r *activeTurnRegistry) Activate(
	ctx context.Context,
	conversationKey, eventID string,
) (context.Context, func()) {
	turnCtx, cancel := context.WithCancelCause(ctx)
	entry := &activeTurn{eventID: eventID, cancel: cancel}

	r.mu.Lock()
	previous := r.current[conversationKey]
	r.current[conversationKey] = entry
	r.mu.Unlock()
	if previous != nil {
		previous.cancel(errTurnSuperseded)
	}

	return turnCtx, func() {
		cancel(nil)
		r.mu.Lock()
		if r.current[conversationKey] == entry {
			delete(r.current, conversationKey)
		}
		r.mu.Unlock()
	}
}

// choiceSender returns provider receipts without exposing transport payloads.
type choiceSender interface {
	SendTracked(context.Context, messaging.Outgoing) (string, error)
	RetireChoices(context.Context, string, string) error
}

func (s *Service) lockStoredConversation(ctx context.Context, conversationID string) (conversation.Conversation, func(), error) {
	conv, err := s.conversations.FindByID(ctx, conversationID)
	if err != nil {
		return conversation.Conversation{}, nil, err
	}
	unlock, err := s.lockConversation(ctx, messaging.Envelope{Provider: conv.Provider, ExternalThreadID: conv.ExternalThreadID}, id.New())
	if err != nil {
		return conversation.Conversation{}, nil, err
	}
	// The assistant could have saved a new draft or handoff while we waited.
	conv, err = s.conversations.FindByID(ctx, conversationID)
	if err != nil {
		unlock()
		return conversation.Conversation{}, nil, err
	}
	return conv, unlock, nil
}

// lockConversation uses the shared lease store so distinct messages cannot
// overwrite one another's draft or interleave replies across Cloud Run instances.
// The enclosing request is bounded below the store's five-minute lease lifetime.
func (s *Service) lockConversation(ctx context.Context, msg messaging.Envelope, owner string) (func(), error) {
	key := fmt.Sprintf("conversation-turn:%x", sha256.Sum256([]byte(conversation.Key(msg.Provider, msg.ExternalThreadID))))
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		claimed, err := s.processed.Claim(ctx, key, owner, s.now())
		if err != nil {
			return nil, fmt.Errorf("lock the conversation: %w", err)
		}
		if claimed {
			return func() { s.releaseDelivery(ctx, key, owner) }, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) retireChoices(ctx context.Context, sender Sender, conv *conversation.Conversation) {
	tracked, ok := sender.(choiceSender)
	if !ok || conv.LastChoiceMessageID == "" {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := tracked.RetireChoices(cleanup, conv.ExternalThreadID, conv.LastChoiceMessageID); err != nil {
		s.logger.WarnContext(ctx, "could not retire the previous choices", "conversation_id", conv.ID, "error", err)
	}
	// Even when cosmetic removal fails, that keyboard is no longer actionable.
	conv.LastChoiceMessageID = ""
}

// retireUntrackedChoices removes a newly delivered keyboard whose provider ID
// could not be persisted. Without this cleanup it would look actionable to the
// customer while every press would necessarily be rejected as stale.
func (s *Service) retireUntrackedChoices(
	ctx context.Context,
	sender choiceSender,
	threadID, messageID, conversationID string,
) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := sender.RetireChoices(cleanup, threadID, messageID); err != nil {
		s.logger.WarnContext(ctx, "could not remove choices whose message id was not stored",
			"error", err, "conversation_id", conversationID)
	}
}

func expiredChoice(lang language) string {
	switch lang {
	case languageArmenian:
		return "Այս ընտրությունն այլևս հասանելի չէ։ Օգտվեք վերջին հաղորդագրությունից կամ գրեք՝ ինչ կցանկանայիք փոխել։"
	case languageRussian:
		return "Этот вариант уже устарел. Используйте последнее сообщение или напишите, что хотите изменить."
	default:
		return "That choice has expired. Use the latest message, or tell me what you would like to change."
	}
}
