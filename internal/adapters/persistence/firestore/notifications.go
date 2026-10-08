package firestore

import (
	"cloud.google.com/go/firestore"
	"context"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

const notificationCollection = "native_booking_notifications"

// TouchNotificationConversation changes activity timestamps only. It preserves
// the assistant draft, staff handoff and conversation revision concurrently.
func (s *Store) TouchNotificationConversation(ctx context.Context, key string, at time.Time) error {
	ref := s.client.Collection(collectionConversations).Doc(key)
	return s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		var stored conversationDoc
		if err = doc.DataTo(&stored); err != nil {
			return err
		}
		if !at.After(stored.LastMessageAt) {
			return nil
		}
		return tx.Update(ref, []firestore.Update{{Path: "last_message_at", Value: at}, {Path: "updated_at", Value: at}})
	})
}

func (s *Store) GetNotification(ctx context.Context, key string) (notifications.Entry, error) {
	doc, err := s.client.Collection(notificationCollection).Doc(key).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return notifications.Entry{}, nil
	}
	if err != nil {
		return notifications.Entry{}, err
	}
	var row notifications.Entry
	err = doc.DataTo(&row)
	return row, err
}
func (s *Store) TransactNotifications(ctx context.Context, keys []string, change func(map[string]*notifications.Entry) error) error {
	return s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		rows := map[string]*notifications.Entry{}
		for _, key := range keys {
			doc, err := tx.Get(s.client.Collection(notificationCollection).Doc(key))
			if err != nil && status.Code(err) != codes.NotFound {
				return err
			}
			row := new(notifications.Entry)
			if err == nil {
				if err = doc.DataTo(row); err != nil {
					return err
				}
			}
			rows[key] = row
		}
		if err := change(rows); err != nil {
			return err
		}
		for _, key := range keys {
			if err := tx.Set(s.client.Collection(notificationCollection).Doc(key), rows[key]); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) PendingNotifications(ctx context.Context) ([]notifications.Entry, error) {
	docs, err := s.client.Collection(notificationCollection).Where("state", "in", []string{"pending", "prepared"}).Limit(100).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	var rows []notifications.Entry
	for _, doc := range docs {
		var row notifications.Entry
		if err = doc.DataTo(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Store) PendingSMSNotifications(ctx context.Context) ([]notifications.Entry, error) {
	docs, err := s.client.Collection(notificationCollection).
		Where("kind", "==", "event").
		Where("outcome", "in", []string{"accepted_sms", "uncertain_sms"}).
		OrderBy("updated_at", firestore.Asc).Limit(20).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	var rows []notifications.Entry
	for _, doc := range docs {
		var row notifications.Entry
		if err := doc.DataTo(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
