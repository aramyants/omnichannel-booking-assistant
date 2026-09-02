// Package memory keeps application state in the process.
//
// It exists so the system can be run and tested without any cloud dependency.
// State does not survive a restart and is not shared between instances, so it
// is for local development and tests only; production uses a durable store
// behind the same interfaces.
package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

// defaultProcessedTTL is how long a handled delivery is remembered.
//
// It bounds memory, and it bounds it safely: providers give up redelivering
// long before this, so an entry can only expire once no retry could still
// arrive for it.
const defaultProcessedTTL = 24 * time.Hour

// Store holds customers, conversations, the transcript and the record of
// handled deliveries.
//
// One mutex guards everything. That is not a performance compromise at this
// size, and it is what makes find-or-create genuinely atomic: two messages
// arriving at once from the same new customer cannot each create a record.
type Store struct {
	mu sync.Mutex

	customers  map[string]customer.Customer
	identities map[string]customer.ChannelIdentity

	// conversations is keyed by channel thread, which is how a conversation is
	// looked up when a message arrives. byID resolves the other direction.
	conversations       map[string]conversation.Conversation
	conversationKeyByID map[string]string

	messages  map[string][]conversation.Message
	processed map[string]time.Time

	// bookings is keyed by customer, which is how a customer's appointments
	// are read back.
	bookings map[string][]booking.Booking

	processedTTL time.Duration
	now          func() time.Time
}

// Option customises a Store.
type Option func(*Store)

// WithClock overrides the clock, so tests can control expiry.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// WithProcessedTTL overrides how long handled deliveries are remembered.
func WithProcessedTTL(ttl time.Duration) Option {
	return func(s *Store) { s.processedTTL = ttl }
}

// New returns an empty Store.
func New(opts ...Option) *Store {
	s := &Store{
		customers:           make(map[string]customer.Customer),
		identities:          make(map[string]customer.ChannelIdentity),
		conversations:       make(map[string]conversation.Conversation),
		conversationKeyByID: make(map[string]string),
		messages:            make(map[string][]conversation.Message),
		processed:           make(map[string]time.Time),
		bookings:            make(map[string][]booking.Booking),
		processedTTL:        defaultProcessedTTL,
		now:                 time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// FindOrCreateByChannelIdentity returns the customer owning identity, creating
// candidate and linking the identity to it when the identity is unknown.
func (s *Store) FindOrCreateByChannelIdentity(
	_ context.Context,
	identity customer.ChannelIdentity,
	candidate customer.Customer,
) (customer.Customer, error) {
	if err := identity.Validate(); err != nil {
		return customer.Customer{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.identities[identity.Key()]; ok {
		return s.customers[existing.CustomerID], nil
	}

	s.customers[candidate.ID] = candidate
	s.identities[identity.Key()] = identity
	return candidate, nil
}

// FindOrOpen returns the conversation on candidate's channel thread, storing
// candidate as a new one if there is none.
func (s *Store) FindOrOpen(
	_ context.Context,
	candidate conversation.Conversation,
) (conversation.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := candidate.Key()
	if existing, ok := s.conversations[key]; ok {
		return existing, nil
	}

	s.conversations[key] = candidate
	s.conversationKeyByID[candidate.ID] = key
	return candidate, nil
}

// Save stores a conversation, replacing any earlier version of it.
func (s *Store) Save(_ context.Context, conv conversation.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.conversations[conv.Key()] = conv
	s.conversationKeyByID[conv.ID] = conv.Key()
	return nil
}

// FindByID returns one conversation.
func (s *Store) FindByID(_ context.Context, conversationID string) (conversation.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.conversationKeyByID[conversationID]
	if !ok {
		return conversation.Conversation{}, conversation.ErrNotFound
	}
	return s.conversations[key], nil
}

// Append adds a message to a conversation's transcript.
func (s *Store) Append(_ context.Context, msg conversation.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages[msg.ConversationID] = append(s.messages[msg.ConversationID], msg)
	return nil
}

// Recent returns up to limit messages, oldest first.
func (s *Store) Recent(_ context.Context, conversationID string, limit int) ([]conversation.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.messages[conversationID]
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}

	// Cloned so a caller cannot mutate stored state through the returned slice,
	// which a durable store would never allow either.
	return slices.Clone(all), nil
}

// SaveBooking records an appointment, replacing any earlier version of it.
func (s *Store) SaveBooking(_ context.Context, b booking.Booking) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.bookings[b.CustomerID]
	for i, stored := range existing {
		if stored.ExternalID == b.ExternalID {
			existing[i] = b
			return nil
		}
	}

	s.bookings[b.CustomerID] = append(existing, b)
	return nil
}

// ListBookings returns a customer's appointments, soonest first.
func (s *Store) ListBookings(_ context.Context, customerID string) ([]booking.Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := slices.Clone(s.bookings[customerID])
	slices.SortFunc(stored, func(a, b booking.Booking) int {
		return a.StartsAt.Compare(b.StartsAt)
	})
	return stored, nil
}

// Seen reports whether a delivery has already been handled.
func (s *Store) Seen(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	at, ok := s.processed[key]
	if !ok {
		return false, nil
	}

	if s.now().Sub(at) > s.processedTTL {
		delete(s.processed, key)
		return false, nil
	}
	return true, nil
}

// MarkProcessed records that a delivery has been handled.
func (s *Store) MarkProcessed(_ context.Context, key string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.processed[key] = at
	s.pruneProcessed()
	return nil
}

// pruneProcessed drops expired entries. The caller must hold the mutex.
func (s *Store) pruneProcessed() {
	now := s.now()
	for key, at := range s.processed {
		if now.Sub(at) > s.processedTTL {
			delete(s.processed, key)
		}
	}
}
