// Package reminders plans and delivers appointment reminders. It contains no
// Cloud Tasks or HTTP details; those live behind Scheduler and Repository.
package reminders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

const (
	// Cloud Tasks cannot schedule more than 30 days ahead. Waking at 29 days
	// leaves a full day for clock skew and retries; an early wake simply chains
	// another deterministic task until the reminder is due.
	maxTaskDelay = 29 * 24 * time.Hour

	// A crashed delivery becomes retryable. Sending and recording that send
	// cannot be one transaction across a messaging provider, so the handler
	// prefers a small duplicate risk over silently losing the reminder.
	claimDuration = 5 * time.Minute
)

// Repository persists reminders and coordinates at-least-once task delivery.
type Repository interface {
	EnsureReminder(ctx context.Context, candidate reminder.Reminder) error
	FindReminder(ctx context.Context, reminderID string) (reminder.Reminder, error)
	ClaimReminder(
		ctx context.Context,
		reminderID, claimID string,
		at, leaseUntil time.Time,
	) (reminder.Reminder, bool, error)
	FinishReminder(ctx context.Context, reminderID, claimID string, status reminder.Status, at time.Time) error
	ReleaseReminder(ctx context.Context, reminderID, claimID string) error
}

// BookingRepository reads the assistant's local view of appointments.
type BookingRepository interface {
	ListBookings(ctx context.Context, customerID string) ([]booking.Booking, error)
}

// MessageRepository records a delivered reminder in the visible transcript.
type MessageRepository interface {
	Append(ctx context.Context, msg conversation.Message) error
}

// Sender delivers a reminder on its original channel.
type Sender interface {
	Send(ctx context.Context, msg messaging.Outgoing) error
}

// Task is one request for the reminder worker to wake up.
type Task struct {
	ID         string
	ReminderID string
	RunAt      time.Time
}

// Scheduler arranges a future call to Deliver.
type Scheduler interface {
	Schedule(ctx context.Context, task Task) error
}

// Deps are the collaborators a Service needs.
type Deps struct {
	Repository Repository
	Bookings   BookingRepository
	Messages   MessageRepository
	Senders    map[messaging.Provider]Sender
	Scheduler  Scheduler
	LeadTime   time.Duration
	Location   *time.Location
	Logger     *slog.Logger
	Now        func() time.Time
}

// Service plans and sends reminders.
type Service struct {
	repository Repository
	bookings   BookingRepository
	messages   MessageRepository
	senders    map[messaging.Provider]Sender
	scheduler  Scheduler
	leadTime   time.Duration
	location   *time.Location
	logger     *slog.Logger
	now        func() time.Time
}

// NewService returns a reminder service with explicit, validated dependencies.
func NewService(deps Deps) (*Service, error) {
	switch {
	case deps.Repository == nil:
		return nil, errors.New("reminders: repository is required")
	case deps.Bookings == nil:
		return nil, errors.New("reminders: booking repository is required")
	case deps.Messages == nil:
		return nil, errors.New("reminders: message repository is required")
	case deps.Scheduler == nil:
		return nil, errors.New("reminders: scheduler is required")
	case deps.LeadTime <= 0:
		return nil, errors.New("reminders: lead time must be positive")
	case deps.Logger == nil:
		return nil, errors.New("reminders: logger is required")
	}

	loc := deps.Location
	if loc == nil {
		loc = time.UTC
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	senders := deps.Senders
	if senders == nil {
		senders = map[messaging.Provider]Sender{}
	}

	return &Service{
		repository: deps.Repository,
		bookings:   deps.Bookings,
		messages:   deps.Messages,
		senders:    senders,
		scheduler:  deps.Scheduler,
		leadTime:   deps.LeadTime,
		location:   loc,
		logger:     deps.Logger,
		now:        now,
	}, nil
}

// Plan records and schedules the reminder for one exact appointment start.
// Calling it twice for the same booking version is harmless: both the reminder
// and task identifiers are deterministic.
func (s *Service) Plan(
	ctx context.Context,
	b booking.Booking,
	conv conversation.Conversation,
) error {
	dueAt := b.StartsAt.Add(-s.leadTime)
	if !dueAt.After(s.now()) {
		// An appointment made inside the lead window should not receive a
		// delayed reminder immediately after the normal booking confirmation.
		return nil
	}

	r := reminder.Reminder{
		ID:                reminderID(b),
		BookingExternalID: b.ExternalID,
		CustomerID:        b.CustomerID,
		ConversationID:    conv.ID,
		Provider:          conv.Provider,
		ExternalThreadID:  conv.ExternalThreadID,
		ExpectedStartsAt:  b.StartsAt,
		DueAt:             dueAt,
		Status:            reminder.StatusScheduled,
		CreatedAt:         s.now(),
	}
	if err := r.Validate(); err != nil {
		return fmt.Errorf("plan reminder: %w", err)
	}
	if err := s.repository.EnsureReminder(ctx, r); err != nil {
		return fmt.Errorf("plan reminder: %w", err)
	}
	return s.scheduleWake(ctx, r, s.now())
}

// Deliver handles an at-least-once task invocation.
func (s *Service) Deliver(ctx context.Context, reminderID string) error {
	r, err := s.repository.FindReminder(ctx, reminderID)
	if err != nil {
		return fmt.Errorf("deliver reminder: %w", err)
	}
	if r.Terminal() {
		return nil
	}

	now := s.now()
	if r.DueAt.After(now) {
		// This is a long-horizon chaining wake, not the actual reminder yet.
		return s.scheduleWake(ctx, r, now)
	}

	claimID := id.New()
	claimed, owned, err := s.repository.ClaimReminder(ctx, r.ID, claimID, now, now.Add(claimDuration))
	if err != nil {
		return fmt.Errorf("deliver reminder: claim: %w", err)
	}
	if !owned {
		return nil
	}

	release := true
	defer func() {
		if release {
			if releaseErr := s.repository.ReleaseReminder(
				context.WithoutCancel(ctx), claimed.ID, claimID,
			); releaseErr != nil {
				s.logger.ErrorContext(ctx, "could not release reminder delivery",
					"error", releaseErr, "reminder_id", claimed.ID)
			}
		}
	}()

	b, current, err := s.currentBooking(ctx, claimed)
	if err != nil {
		return fmt.Errorf("deliver reminder: read booking: %w", err)
	}
	if !current {
		release = false
		return s.finish(ctx, claimed, claimID, reminder.StatusSkipped, now)
	}

	sender, ok := s.senders[claimed.Provider]
	if !ok {
		return fmt.Errorf("deliver reminder: no sender configured for %s", claimed.Provider)
	}

	text := fmt.Sprintf("Reminder: your appointment is %s at %s. Reference: %s.",
		b.StartsAt.In(s.location).Format("Monday 2 January 2006"),
		b.StartsAt.In(s.location).Format("15:04"),
		b.ExternalID,
	)
	if err := sender.Send(ctx, messaging.Outgoing{
		Provider:         claimed.Provider,
		ExternalThreadID: claimed.ExternalThreadID,
		Text:             text,
	}); err != nil {
		return fmt.Errorf("deliver reminder: send: %w", err)
	}

	// The external send has happened. Never return an error after this point:
	// Cloud Tasks would retry and could send the customer a duplicate.
	release = false
	if err := s.finish(ctx, claimed, claimID, reminder.StatusSent, now); err != nil {
		s.logger.ErrorContext(ctx, "sent a reminder but could not mark it complete",
			"error", err, "reminder_id", claimed.ID)
	}
	if err := s.messages.Append(ctx, conversation.Message{
		ID:             id.New(),
		ConversationID: claimed.ConversationID,
		Direction:      conversation.DirectionOutbound,
		ContentType:    messaging.ContentTypeText,
		Text:           text,
		CreatedAt:      now,
	}); err != nil {
		s.logger.ErrorContext(ctx, "sent a reminder but could not record it in the transcript",
			"error", err, "reminder_id", claimed.ID)
	}
	return nil
}

func (s *Service) currentBooking(
	ctx context.Context,
	r reminder.Reminder,
) (booking.Booking, bool, error) {
	booked, err := s.bookings.ListBookings(ctx, r.CustomerID)
	if err != nil {
		return booking.Booking{}, false, err
	}
	for _, b := range booked {
		if b.ExternalID != r.BookingExternalID {
			continue
		}
		return b, b.Status == booking.StatusConfirmed &&
			b.StartsAt.Equal(r.ExpectedStartsAt) && b.StartsAt.After(s.now()), nil
	}
	return booking.Booking{}, false, nil
}

func (s *Service) finish(
	ctx context.Context,
	r reminder.Reminder,
	claimID string,
	status reminder.Status,
	at time.Time,
) error {
	if err := s.repository.FinishReminder(ctx, r.ID, claimID, status, at); err != nil {
		return fmt.Errorf("finish reminder: %w", err)
	}
	return nil
}

func (s *Service) scheduleWake(ctx context.Context, r reminder.Reminder, now time.Time) error {
	runAt := r.DueAt
	if horizon := now.Add(maxTaskDelay); runAt.After(horizon) {
		runAt = horizon
	}
	task := Task{
		ID:         taskID(r.ID, runAt),
		ReminderID: r.ID,
		RunAt:      runAt,
	}
	if err := s.scheduler.Schedule(ctx, task); err != nil {
		return fmt.Errorf("schedule reminder wake: %w", err)
	}
	return nil
}

func reminderID(b booking.Booking) string {
	return hashID("reminder", b.ID+"\x00"+b.ExternalID+"\x00"+b.StartsAt.UTC().Format(time.RFC3339Nano))
}

func taskID(reminderID string, runAt time.Time) string {
	return hashID("reminder-task", reminderID+"\x00"+runAt.UTC().Format(time.RFC3339Nano))
}

func hashID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(sum[:16])
}
