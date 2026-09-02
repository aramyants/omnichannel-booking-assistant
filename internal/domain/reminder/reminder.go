// Package reminder models delayed appointment notifications independently of
// the system that wakes the application to send them.
package reminder

import (
	"errors"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// ErrNotFound reports a reminder that does not exist.
var ErrNotFound = errors.New("reminder not found")

// Status records whether a reminder can still be delivered.
type Status string

const (
	StatusScheduled  Status = "scheduled"
	StatusDelivering Status = "delivering"
	StatusSent       Status = "sent"
	StatusSkipped    Status = "skipped"
)

// Reminder is one notification tied to one exact version of an appointment.
// ExpectedStartsAt is deliberately stored: if the appointment is rescheduled,
// the old task wakes up, sees a different start, and sends nothing.
type Reminder struct {
	ID                string
	BookingExternalID string
	CustomerID        string
	ConversationID    string
	Provider          messaging.Provider
	ExternalThreadID  string
	ExpectedStartsAt  time.Time
	DueAt             time.Time
	Status            Status
	CreatedAt         time.Time

	ClaimID      string
	ClaimExpires time.Time
	FinishedAt   time.Time
}

// Validate reports whether a reminder has everything delivery depends on.
func (r Reminder) Validate() error {
	switch {
	case r.ID == "":
		return errors.New("reminder id is empty")
	case r.BookingExternalID == "":
		return errors.New("booking reference is empty")
	case r.CustomerID == "":
		return errors.New("customer id is empty")
	case r.ConversationID == "":
		return errors.New("conversation id is empty")
	case r.Provider == "":
		return errors.New("provider is empty")
	case r.ExternalThreadID == "":
		return errors.New("thread id is empty")
	case r.ExpectedStartsAt.IsZero():
		return errors.New("expected appointment start is empty")
	case r.DueAt.IsZero():
		return errors.New("due time is empty")
	case r.Status != StatusScheduled:
		return fmt.Errorf("new reminder status must be %q: got %q", StatusScheduled, r.Status)
	case r.CreatedAt.IsZero():
		return errors.New("creation time is empty")
	}
	return nil
}

// Terminal reports whether another task delivery has any work to do.
func (r Reminder) Terminal() bool {
	return r.Status == StatusSent || r.Status == StatusSkipped
}
