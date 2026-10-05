package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/staffinbox"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (s *Store) TelegramConversations(_ context.Context, before *staffinbox.Cursor, limit int) (staffinbox.Conversations, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit = inboxLimit(limit, staffinbox.ConversationLimit)
	var items []conversation.Conversation
	for _, conv := range s.conversations {
		if conv.Provider != messaging.ProviderTelegram {
			continue
		}
		if before != nil && (conv.LastMessageAt.After(before.LastMessageAt) ||
			(conv.LastMessageAt.Equal(before.LastMessageAt) && conv.Key() >= before.Key)) {
			continue
		}
		items = append(items, conv)
	}
	slices.SortFunc(items, func(a, b conversation.Conversation) int {
		if order := b.LastMessageAt.Compare(a.LastMessageAt); order != 0 {
			return order
		}
		return strings.Compare(b.Key(), a.Key())
	})
	page := staffinbox.Conversations{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.Next = &staffinbox.Cursor{LastMessageAt: last.LastMessageAt, Key: last.Key()}
	}
	return page, nil
}

func inboxLimit(limit, maximum int) int {
	if limit <= 0 || limit > maximum {
		return maximum
	}
	return limit
}

func (s *Store) InboxCustomer(_ context.Context, customerID string) (customer.Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.customers[customerID], nil
}

func (s *Store) InboxHistory(_ context.Context, conversationID, before string, limit int) (staffinbox.History, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit = inboxLimit(limit, staffinbox.HistoryLimit)
	var items []conversation.Message
	for _, msg := range s.messages[conversationID] {
		if before == "" || msg.ID < before {
			items = append(items, msg)
		}
	}
	slices.SortFunc(items, func(a, b conversation.Message) int { return strings.Compare(b.ID, a.ID) })
	page := staffinbox.History{Messages: items}
	if len(items) > limit {
		page.Messages = items[:limit]
		page.Before = page.Messages[limit-1].ID
	}
	slices.Reverse(page.Messages)
	return page, nil
}

func (s *Store) SaveInboxPage(_ context.Context, page staffinbox.Page) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inboxPages == nil {
		s.inboxPages = make(map[string]staffinbox.Page)
	}
	for key, stored := range s.inboxPages {
		if !stored.ExpiresAt.After(s.now()) {
			delete(s.inboxPages, key)
		}
	}
	s.inboxPages[page.ID] = page
	return nil
}

func (s *Store) InboxPage(_ context.Context, pageID string) (staffinbox.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, ok := s.inboxPages[pageID]
	if !ok || !page.ExpiresAt.After(s.now()) {
		return staffinbox.Page{}, staffinbox.ErrPageExpired
	}
	return page, nil
}
