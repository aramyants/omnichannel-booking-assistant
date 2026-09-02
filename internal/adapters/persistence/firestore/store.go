// Package firestore keeps application state in Google Cloud Firestore.
//
// It implements the same interfaces as the in-process store, so nothing in the
// application layer knows which one it is talking to. Firestore is used rather
// than a relational database because it costs nothing at rest, which matters
// for a service that spends most of its life idle.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Collection names. Documents are keyed by a natural identifier wherever one
// exists, so a lookup is a direct read rather than a query: reads by key are
// cheaper, faster and strongly consistent, and queries are neither.
const (
	collectionCustomers     = "customers"
	collectionIdentities    = "channel_identities"
	collectionConversations = "conversations"
	collectionMessages      = "messages"
	collectionProcessed     = "processed_events"
	collectionBookings      = "bookings"
)

// processedRetention is how long a handled delivery is remembered.
//
// Documents carry an expiry field, and a Firestore TTL policy on that field
// deletes them. Providers give up redelivering long before this, so an entry
// can only expire once no retry could still arrive for it.
const (
	processedRetention = 7 * 24 * time.Hour
	claimRetention     = 5 * time.Minute
)

// Store is a Firestore-backed implementation of the application's repositories.
type Store struct {
	client *firestore.Client
}

// New returns a Store backed by projectID.
//
// The caller owns the returned Close.
func New(ctx context.Context, projectID string) (*Store, error) {
	if projectID == "" {
		return nil, errors.New("firestore: project id is required")
	}

	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("firestore: connect to %s: %w", projectID, err)
	}

	return &Store{client: client}, nil
}

// Close releases the underlying connection.
func (s *Store) Close() error { return s.client.Close() }

// customerDoc is how a customer is stored. The document shape is deliberately
// separate from the domain type: renaming a Go field should not silently
// orphan every record already written.
type customerDoc struct {
	Name      string    `firestore:"name"`
	Phone     string    `firestore:"phone"`
	CreatedAt time.Time `firestore:"created_at"`
	UpdatedAt time.Time `firestore:"updated_at"`
}

type identityDoc struct {
	CustomerID     string    `firestore:"customer_id"`
	Provider       string    `firestore:"provider"`
	ExternalUserID string    `firestore:"external_user_id"`
	DisplayName    string    `firestore:"display_name"`
	Language       string    `firestore:"language"`
	CreatedAt      time.Time `firestore:"created_at"`
}

// FindOrCreateByChannelIdentity returns the customer owning identity, creating
// candidate and linking the identity to it when the identity is unknown.
//
// It runs in a transaction. Without one, a customer sending two messages at
// once would be created twice and their history split across two records: both
// requests would read "no such identity" before either wrote one.
func (s *Store) FindOrCreateByChannelIdentity(
	ctx context.Context,
	identity customer.ChannelIdentity,
	candidate customer.Customer,
) (customer.Customer, error) {
	if err := identity.Validate(); err != nil {
		return customer.Customer{}, err
	}

	identityRef := s.client.Collection(collectionIdentities).Doc(identity.Key())
	resolved := candidate

	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(identityRef)

		switch {
		case err == nil:
			var existing identityDoc
			if err := snapshot.DataTo(&existing); err != nil {
				return err
			}

			customerSnapshot, err := tx.Get(s.client.Collection(collectionCustomers).Doc(existing.CustomerID))
			if err != nil {
				// The identity points at a customer that is gone. Treating this
				// as unknown rebuilds the link rather than failing every message
				// this person sends from now on.
				if status.Code(err) == codes.NotFound {
					break
				}
				return err
			}

			var doc customerDoc
			if err := customerSnapshot.DataTo(&doc); err != nil {
				return err
			}
			resolved = customer.Customer{
				ID:        existing.CustomerID,
				Name:      doc.Name,
				Phone:     doc.Phone,
				CreatedAt: doc.CreatedAt,
				UpdatedAt: doc.UpdatedAt,
			}
			return nil

		case status.Code(err) != codes.NotFound:
			return err
		}

		// Unknown identity: create the customer and the link together.
		resolved = candidate

		if err := tx.Set(s.client.Collection(collectionCustomers).Doc(candidate.ID), customerDoc{
			Name:      candidate.Name,
			Phone:     candidate.Phone,
			CreatedAt: candidate.CreatedAt,
			UpdatedAt: candidate.UpdatedAt,
		}); err != nil {
			return err
		}

		return tx.Set(identityRef, identityDoc{
			CustomerID:     candidate.ID,
			Provider:       string(identity.Provider),
			ExternalUserID: identity.ExternalUserID,
			DisplayName:    identity.DisplayName,
			Language:       identity.Language,
			CreatedAt:      identity.CreatedAt,
		})
	})
	if err != nil {
		return customer.Customer{}, fmt.Errorf("firestore: resolve customer: %w", err)
	}

	return resolved, nil
}

type draftDoc struct {
	IdempotencyKey string    `firestore:"idempotency_key"`
	ServiceIDs     []string  `firestore:"service_ids"`
	ServiceNames   []string  `firestore:"service_names"`
	StaffID        string    `firestore:"staff_id"`
	StaffName      string    `firestore:"staff_name"`
	StartsAt       time.Time `firestore:"starts_at"`
	DurationSecs   int64     `firestore:"duration_seconds"`
	Phone          string    `firestore:"phone"`
	CustomerName   string    `firestore:"customer_name"`
	PreparedAt     time.Time `firestore:"prepared_at"`
}

type conversationDoc struct {
	ID               string    `firestore:"id"`
	CustomerID       string    `firestore:"customer_id"`
	Provider         string    `firestore:"provider"`
	ExternalThreadID string    `firestore:"external_thread_id"`
	State            string    `firestore:"state"`
	Draft            *draftDoc `firestore:"draft"`
	CreatedAt        time.Time `firestore:"created_at"`
	UpdatedAt        time.Time `firestore:"updated_at"`
	LastMessageAt    time.Time `firestore:"last_message_at"`
}

func toConversationDoc(conv conversation.Conversation) conversationDoc {
	doc := conversationDoc{
		ID:               conv.ID,
		CustomerID:       conv.CustomerID,
		Provider:         string(conv.Provider),
		ExternalThreadID: conv.ExternalThreadID,
		State:            string(conv.State),
		CreatedAt:        conv.CreatedAt,
		UpdatedAt:        conv.UpdatedAt,
		LastMessageAt:    conv.LastMessageAt,
	}

	if conv.Draft != nil {
		doc.Draft = &draftDoc{
			IdempotencyKey: conv.Draft.IdempotencyKey,
			ServiceIDs:     conv.Draft.ServiceIDs,
			ServiceNames:   conv.Draft.ServiceNames,
			StaffID:        conv.Draft.StaffID,
			StaffName:      conv.Draft.StaffName,
			StartsAt:       conv.Draft.StartsAt,
			DurationSecs:   int64(conv.Draft.Duration.Seconds()),
			Phone:          conv.Draft.Phone,
			CustomerName:   conv.Draft.CustomerName,
			PreparedAt:     conv.Draft.PreparedAt,
		}
	}
	return doc
}

func fromConversationDoc(doc conversationDoc) conversation.Conversation {
	conv := conversation.Conversation{
		ID:               doc.ID,
		CustomerID:       doc.CustomerID,
		Provider:         messaging.Provider(doc.Provider),
		ExternalThreadID: doc.ExternalThreadID,
		State:            conversation.State(doc.State),
		CreatedAt:        doc.CreatedAt,
		UpdatedAt:        doc.UpdatedAt,
		LastMessageAt:    doc.LastMessageAt,
	}

	if doc.Draft != nil {
		conv.Draft = &booking.Draft{
			IdempotencyKey: doc.Draft.IdempotencyKey,
			ServiceIDs:     doc.Draft.ServiceIDs,
			ServiceNames:   doc.Draft.ServiceNames,
			StaffID:        doc.Draft.StaffID,
			StaffName:      doc.Draft.StaffName,
			StartsAt:       doc.Draft.StartsAt,
			Duration:       time.Duration(doc.Draft.DurationSecs) * time.Second,
			Phone:          doc.Draft.Phone,
			CustomerName:   doc.Draft.CustomerName,
			PreparedAt:     doc.Draft.PreparedAt,
		}
	}
	return conv
}

// FindOrOpen returns the conversation on candidate's channel thread, storing
// candidate as a new one if there is none.
func (s *Store) FindOrOpen(
	ctx context.Context,
	candidate conversation.Conversation,
) (conversation.Conversation, error) {
	ref := s.client.Collection(collectionConversations).Doc(candidate.Key())
	resolved := candidate

	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(ref)

		if err == nil {
			var doc conversationDoc
			if err := snapshot.DataTo(&doc); err != nil {
				return err
			}
			resolved = fromConversationDoc(doc)
			return nil
		}
		if status.Code(err) != codes.NotFound {
			return err
		}

		resolved = candidate
		return tx.Set(ref, toConversationDoc(candidate))
	})
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("firestore: open conversation: %w", err)
	}

	return resolved, nil
}

// Save stores a conversation, replacing any earlier version of it.
func (s *Store) Save(ctx context.Context, conv conversation.Conversation) error {
	_, err := s.client.Collection(collectionConversations).Doc(conv.Key()).Set(ctx, toConversationDoc(conv))
	if err != nil {
		return fmt.Errorf("firestore: save conversation: %w", err)
	}
	return nil
}

// FindByID returns one conversation.
func (s *Store) FindByID(ctx context.Context, conversationID string) (conversation.Conversation, error) {
	docs, err := s.client.Collection(collectionConversations).
		Where("id", "==", conversationID).
		Limit(1).
		Documents(ctx).
		GetAll()
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("firestore: find conversation: %w", err)
	}
	if len(docs) == 0 {
		return conversation.Conversation{}, conversation.ErrNotFound
	}

	var doc conversationDoc
	if err := docs[0].DataTo(&doc); err != nil {
		return conversation.Conversation{}, fmt.Errorf("firestore: read conversation: %w", err)
	}
	return fromConversationDoc(doc), nil
}

type messageDoc struct {
	ID                string    `firestore:"id"`
	SortID            string    `firestore:"sort_id"`
	ConversationID    string    `firestore:"conversation_id"`
	Direction         string    `firestore:"direction"`
	ContentType       string    `firestore:"content_type"`
	Text              string    `firestore:"text"`
	ExternalMessageID string    `firestore:"external_message_id"`
	CreatedAt         time.Time `firestore:"created_at"`
}

// Append adds a message to a conversation's transcript.
//
// Messages live in a subcollection keyed by their own identifier. Those are
// UUIDv7, which sort chronologically, so the transcript reads back in order
// from the document key alone.
func (s *Store) Append(ctx context.Context, msg conversation.Message) error {
	_, err := s.client.
		Collection(collectionMessages).Doc(msg.ConversationID).
		Collection(collectionMessages).Doc(msg.ID).
		Set(ctx, messageDoc{
			ID:                msg.ID,
			SortID:            msg.ID,
			ConversationID:    msg.ConversationID,
			Direction:         string(msg.Direction),
			ContentType:       string(msg.ContentType),
			Text:              msg.Text,
			ExternalMessageID: msg.ExternalMessageID,
			CreatedAt:         msg.CreatedAt,
		})
	if err != nil {
		return fmt.Errorf("firestore: append message: %w", err)
	}
	return nil
}

// Recent returns up to limit messages, oldest first.
//
// UUIDv7 ids sort chronologically. Reading the greatest ids first reaches the
// newest part of the transcript without paging through its whole history; the
// result is then reversed for the AI context, which reads oldest to newest.
//
// The id is repeated in sort_id because the Firestore emulator cannot perform
// descending document-key scans. Keeping emulator and production on the same
// tested query is more useful than maintaining an emulator-only code path.
func (s *Store) Recent(ctx context.Context, conversationID string, limit int) ([]conversation.Message, error) {
	if limit <= 0 {
		limit = 50
	}

	docs, err := s.client.
		Collection(collectionMessages).Doc(conversationID).
		Collection(collectionMessages).
		OrderBy("sort_id", firestore.Desc).
		Limit(limit).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, fmt.Errorf("firestore: read transcript: %w", err)
	}

	messages := make([]conversation.Message, 0, len(docs))
	for _, snapshot := range docs {
		var doc messageDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return nil, fmt.Errorf("firestore: read message: %w", err)
		}
		messages = append(messages, conversation.Message{
			ID:                doc.ID,
			ConversationID:    doc.ConversationID,
			Direction:         conversation.Direction(doc.Direction),
			ContentType:       messaging.ContentType(doc.ContentType),
			Text:              doc.Text,
			ExternalMessageID: doc.ExternalMessageID,
			CreatedAt:         doc.CreatedAt,
		})
	}

	slices.Reverse(messages)
	return messages, nil
}

type processedDoc struct {
	ProcessedAt time.Time `firestore:"processed_at"`
	ClaimID     string    `firestore:"claim_id"`

	// ExpiresAt is read by a Firestore TTL policy, which deletes the document
	// once it passes. Without the policy configured these accumulate forever.
	ExpiresAt time.Time `firestore:"expires_at"`
}

// Claim atomically acquires a processing lease for one delivery.
func (s *Store) Claim(ctx context.Context, key, claimID string, at time.Time) (bool, error) {
	ref := s.client.Collection(collectionProcessed).Doc(key)
	claimed := false

	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(ref)
		if err == nil {
			var doc processedDoc
			if err := snapshot.DataTo(&doc); err != nil {
				return err
			}
			if doc.ExpiresAt.After(at) {
				return nil
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}

		claimed = true
		return tx.Set(ref, processedDoc{
			ClaimID:   claimID,
			ExpiresAt: at.Add(claimRetention),
		})
	})
	if err != nil {
		return false, fmt.Errorf("firestore: claim delivery: %w", err)
	}
	return claimed, nil
}

// Complete turns a processing lease into a completed-delivery record.
func (s *Store) Complete(ctx context.Context, key, claimID string, at time.Time) error {
	ref := s.client.Collection(collectionProcessed).Doc(key)
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(ref)
		if err != nil {
			return err
		}

		var doc processedDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return err
		}
		if doc.ClaimID != claimID {
			return errors.New("claim is not owned by this request")
		}

		return tx.Set(ref, processedDoc{
			ProcessedAt: at,
			ExpiresAt:   at.Add(processedRetention),
		})
	})
	if err != nil {
		return fmt.Errorf("firestore: complete delivery: %w", err)
	}
	return nil
}

// Release gives a failed attempt back to the provider for an immediate retry.
func (s *Store) Release(ctx context.Context, key, claimID string) error {
	ref := s.client.Collection(collectionProcessed).Doc(key)
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}

		var doc processedDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return err
		}
		if doc.ClaimID != claimID || !doc.ProcessedAt.IsZero() {
			return nil
		}
		return tx.Delete(ref)
	})
	if err != nil {
		return fmt.Errorf("firestore: release delivery: %w", err)
	}
	return nil
}

type bookingDoc struct {
	ID           string    `firestore:"id"`
	ExternalID   string    `firestore:"external_id"`
	CustomerID   string    `firestore:"customer_id"`
	ServiceIDs   []string  `firestore:"service_ids"`
	StaffID      string    `firestore:"staff_id"`
	StartsAt     time.Time `firestore:"starts_at"`
	DurationSecs int64     `firestore:"duration_seconds"`
	Status       string    `firestore:"status"`
	CreatedAt    time.Time `firestore:"created_at"`
}

// SaveBooking records an appointment.
//
// The document is keyed by the scheduling system's own identifier, so writing
// the same appointment twice updates one record rather than creating two.
func (s *Store) SaveBooking(ctx context.Context, b booking.Booking) error {
	_, err := s.client.Collection(collectionBookings).Doc(b.ExternalID).Set(ctx, bookingDoc{
		ID:           b.ID,
		ExternalID:   b.ExternalID,
		CustomerID:   b.CustomerID,
		ServiceIDs:   b.ServiceIDs,
		StaffID:      b.StaffID,
		StartsAt:     b.StartsAt,
		DurationSecs: int64(b.Duration.Seconds()),
		Status:       string(b.Status),
		CreatedAt:    b.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("firestore: record a booking: %w", err)
	}
	return nil
}

// ListBookings returns a customer's appointments, soonest first.
func (s *Store) ListBookings(ctx context.Context, customerID string) ([]booking.Booking, error) {
	docs, err := s.client.Collection(collectionBookings).
		Where("customer_id", "==", customerID).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, fmt.Errorf("firestore: list bookings: %w", err)
	}

	bookings := make([]booking.Booking, 0, len(docs))
	for _, snapshot := range docs {
		var doc bookingDoc
		if err := snapshot.DataTo(&doc); err != nil {
			return nil, fmt.Errorf("firestore: read booking: %w", err)
		}
		bookings = append(bookings, booking.Booking{
			ID:         doc.ID,
			ExternalID: doc.ExternalID,
			CustomerID: doc.CustomerID,
			ServiceIDs: doc.ServiceIDs,
			StaffID:    doc.StaffID,
			StartsAt:   doc.StartsAt,
			Duration:   time.Duration(doc.DurationSecs) * time.Second,
			Status:     booking.Status(doc.Status),
			CreatedAt:  doc.CreatedAt,
		})
	}

	// Ordering after the equality query avoids requiring a composite production
	// index for what is a very small per-customer result set.
	slices.SortFunc(bookings, func(a, b booking.Booking) int {
		return a.StartsAt.Compare(b.StartsAt)
	})
	return bookings, nil
}
