package firestore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/staffinbox"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const collectionInboxPages = "staff_inbox_pages"

type inboxPageDoc struct {
	ID                string             `firestore:"id"`
	ChatID            string             `firestore:"chat_id"`
	ConversationID    string             `firestore:"conversation_id"`
	NextConversations *staffinbox.Cursor `firestore:"next_conversations"`
	HistoryBefore     string             `firestore:"history_before"`
	HistoryStart      string             `firestore:"history_start"`
	PreviousPageID    string             `firestore:"previous_page_id"`
	ExpiresAt         time.Time          `firestore:"expires_at"`
}

func inboxLimit(limit, maximum int) int {
	if limit <= 0 || limit > maximum {
		return maximum
	}
	return limit
}

func (s *Store) TelegramConversations(ctx context.Context, before *staffinbox.Cursor, limit int) (staffinbox.Conversations, error) {
	limit = inboxLimit(limit, staffinbox.ConversationLimit)
	collection := s.client.Collection(collectionConversations)
	query := collection.Where("provider", "==", string(messaging.ProviderTelegram)).
		OrderBy("last_message_at", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	if before != nil {
		query = query.StartAfter(before.LastMessageAt, collection.Doc(before.Key))
	}
	docs, err := query.Limit(limit + 1).Documents(ctx).GetAll()
	if err != nil {
		return staffinbox.Conversations{}, fmt.Errorf("firestore: list Telegram conversations: %w", err)
	}
	page := staffinbox.Conversations{}
	if len(docs) > limit {
		docs = docs[:limit]
		last := docs[limit-1]
		var doc conversationDoc
		if err := last.DataTo(&doc); err != nil {
			return page, err
		}
		page.Next = &staffinbox.Cursor{LastMessageAt: doc.LastMessageAt, Key: last.Ref.ID}
	}
	for _, snapshot := range docs {
		var doc conversationDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return page, err
		}
		page.Items = append(page.Items, fromConversationDoc(doc))
	}
	return page, nil
}

func (s *Store) InboxCustomer(ctx context.Context, customerID string) (customer.Customer, error) {
	snapshot, err := s.client.Collection(collectionCustomers).Doc(customerID).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return customer.Customer{ID: customerID}, nil
	}
	if err != nil {
		return customer.Customer{}, err
	}
	var doc customerDoc
	if err := snapshot.DataTo(&doc); err != nil {
		return customer.Customer{}, err
	}
	return customer.Customer{ID: customerID, Name: doc.Name, Phone: doc.Phone, CreatedAt: doc.CreatedAt, UpdatedAt: doc.UpdatedAt}, nil
}

func (s *Store) InboxHistory(ctx context.Context, conversationID, before string, limit int) (staffinbox.History, error) {
	limit = inboxLimit(limit, staffinbox.HistoryLimit)
	query := s.client.Collection(collectionMessages).Doc(conversationID).
		Collection(collectionMessages).OrderBy("sort_id", firestore.Desc)
	if before != "" {
		query = query.StartAfter(before)
	}
	docs, err := query.Limit(limit + 1).Documents(ctx).GetAll()
	if err != nil {
		return staffinbox.History{}, fmt.Errorf("firestore: read inbox history: %w", err)
	}
	page := staffinbox.History{}
	if len(docs) > limit {
		docs = docs[:limit]
		var doc messageDoc
		if err := docs[limit-1].DataTo(&doc); err != nil {
			return page, err
		}
		page.Before = doc.SortID
	}
	for _, snapshot := range docs {
		var doc messageDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return page, err
		}
		page.Messages = append(page.Messages, conversation.Message{
			ID: doc.ID, ConversationID: doc.ConversationID,
			Direction: conversation.Direction(doc.Direction), ContentType: messaging.ContentType(doc.ContentType),
			Text: doc.Text, ExternalMessageID: doc.ExternalMessageID, CreatedAt: doc.CreatedAt,
		})
	}
	slices.Reverse(page.Messages)
	return page, nil
}

func (s *Store) SaveInboxPage(ctx context.Context, page staffinbox.Page) error {
	doc := inboxPageDoc(page)
	_, err := s.client.Collection(collectionInboxPages).Doc(page.ID).Set(ctx, doc)
	return err
}

func (s *Store) InboxPage(ctx context.Context, pageID string) (staffinbox.Page, error) {
	snapshot, err := s.client.Collection(collectionInboxPages).Doc(pageID).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return staffinbox.Page{}, staffinbox.ErrPageExpired
	}
	if err != nil {
		return staffinbox.Page{}, err
	}
	var doc inboxPageDoc
	if err := snapshot.DataTo(&doc); err != nil {
		return staffinbox.Page{}, err
	}
	page := staffinbox.Page(doc)
	if !page.ExpiresAt.After(time.Now()) {
		return staffinbox.Page{}, staffinbox.ErrPageExpired
	}
	return page, nil
}
