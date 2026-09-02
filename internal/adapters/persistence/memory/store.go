// Package memory keeps application state in the process.
//
// It exists so the system can be run and tested without any cloud dependency.
// State does not survive a restart and is not shared between instances, so it
// is for local development and tests only; production uses a durable store
// behind the same interfaces.
package memory

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
)

// defaultProcessedTTL is how long a handled delivery is remembered.
//
// It bounds memory, and it bounds it safely: providers give up redelivering
// long before this, so an entry can only expire once no retry could still
// arrive for it.
const (
	defaultProcessedTTL = 24 * time.Hour
	defaultClaimTTL     = 5 * time.Minute
)

type processedEntry struct {
	claimID     string
	processedAt time.Time
	expiresAt   time.Time
}

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
	processed map[string]processedEntry

	// bookings is keyed by customer, which is how a customer's appointments
	// are read back.
	bookings  map[string][]booking.Booking
	reminders map[string]reminder.Reminder

	processedTTL time.Duration
	claimTTL     time.Duration
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

// WithClaimTTL overrides how long an abandoned processing lease is held.
func WithClaimTTL(ttl time.Duration) Option {
	return func(s *Store) { s.claimTTL = ttl }
}

// New returns an empty Store.
func New(opts ...Option) *Store {
	s := &Store{
		customers:           make(map[string]customer.Customer),
		identities:          make(map[string]customer.ChannelIdentity),
		conversations:       make(map[string]conversation.Conversation),
		conversationKeyByID: make(map[string]string),
		messages:            make(map[string][]conversation.Message),
		processed:           make(map[string]processedEntry),
		bookings:            make(map[string][]booking.Booking),
		reminders:           make(map[string]reminder.Reminder),
		processedTTL:        defaultProcessedTTL,
		claimTTL:            defaultClaimTTL,
		now:                 time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnsureReminder stores candidate unless that deterministic reminder id is
// already present. A repeated booking confirmation must not reset a reminder
// that has already been sent.
func (s *Store) EnsureReminder(_ context.Context, candidate reminder.Reminder) error {
	if err := candidate.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reminders[candidate.ID]; !exists {
		s.reminders[candidate.ID] = candidate
	}
	return nil
}

// FindReminder returns one reminder.
func (s *Store) FindReminder(_ context.Context, reminderID string) (reminder.Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reminders[reminderID]
	if !ok {
		return reminder.Reminder{}, reminder.ErrNotFound
	}
	return r, nil
}

// ClaimReminder acquires or recovers a delivery lease.
func (s *Store) ClaimReminder(
	_ context.Context,
	reminderID, claimID string,
	at, leaseUntil time.Time,
) (reminder.Reminder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.reminders[reminderID]
	if !ok {
		return reminder.Reminder{}, false, reminder.ErrNotFound
	}
	if r.Terminal() || (r.Status == reminder.StatusDelivering && r.ClaimExpires.After(at)) {
		return r, false, nil
	}
	r.Status = reminder.StatusDelivering
	r.ClaimID = claimID
	r.ClaimExpires = leaseUntil
	s.reminders[reminderID] = r
	return r, true, nil
}

// FinishReminder records the terminal result owned by claimID.
func (s *Store) FinishReminder(
	_ context.Context,
	reminderID, claimID string,
	status reminder.Status,
	at time.Time,
) error {
	if status != reminder.StatusSent && status != reminder.StatusSkipped {
		return errors.New("finish reminder: status must be sent or skipped")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reminders[reminderID]
	if !ok {
		return reminder.ErrNotFound
	}
	if r.ClaimID != claimID || r.Status != reminder.StatusDelivering {
		return errors.New("finish reminder: claim is not owned by this delivery")
	}
	r.Status = status
	r.FinishedAt = at
	r.ClaimID = ""
	r.ClaimExpires = time.Time{}
	s.reminders[reminderID] = r
	return nil
}

// ReleaseReminder makes a failed delivery retryable immediately.
func (s *Store) ReleaseReminder(_ context.Context, reminderID, claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reminders[reminderID]
	if !ok {
		return reminder.ErrNotFound
	}
	if r.ClaimID != claimID || r.Status != reminder.StatusDelivering {
		return nil
	}
	r.Status = reminder.StatusScheduled
	r.ClaimID = ""
	r.ClaimExpires = time.Time{}
	s.reminders[reminderID] = r
	return nil
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

// Claim atomically acquires a processing lease for one delivery.
func (s *Store) Claim(_ context.Context, key, claimID string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.processed[key]
	if ok && entry.expiresAt.After(at) {
		return false, nil
	}

	s.processed[key] = processedEntry{
		claimID:   claimID,
		expiresAt: at.Add(s.claimTTL),
	}
	return true, nil
}

// Complete turns a processing lease into a completed-delivery record.
func (s *Store) Complete(_ context.Context, key, claimID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.processed[key]
	if !ok || entry.claimID != claimID {
		return errors.New("complete delivery: claim is not owned by this request")
	}

	s.processed[key] = processedEntry{
		processedAt: at,
		expiresAt:   at.Add(s.processedTTL),
	}
	s.pruneProcessed()
	return nil
}

// Release gives a failed attempt back to the provider for an immediate retry.
func (s *Store) Release(_ context.Context, key, claimID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.processed[key]
	if ok && entry.claimID == claimID && entry.processedAt.IsZero() {
		delete(s.processed, key)
	}
	return nil
}

// pruneProcessed drops expired entries. The caller must hold the mutex.
func (s *Store) pruneProcessed() {
	now := s.now()
	for key, entry := range s.processed {
		if !entry.expiresAt.After(now) {
			delete(s.processed, key)
		}
	}
}
