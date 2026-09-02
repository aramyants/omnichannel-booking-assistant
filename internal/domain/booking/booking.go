// Package booking models the scheduling concepts the assistant works with,
// independently of the system that actually holds the calendar.
package booking

import (
	"errors"
	"fmt"
	"time"
)

// Errors the scheduling system can report, in terms the application acts on.
// Adapters translate provider-specific failures into these.
var (
	// ErrSlotUnavailable means the chosen time is no longer free. Availability
	// is a snapshot, never a reservation: between showing a customer a slot and
	// booking it, somebody else can take it.
	ErrSlotUnavailable = errors.New("slot is no longer available")

	// ErrNotFound means the service, staff member or booking does not exist.
	ErrNotFound = errors.New("not found")

	// ErrRejected means the scheduling system refused the request for a reason
	// that repeating it will not fix.
	ErrRejected = errors.New("scheduling system rejected the request")

	// ErrUnavailable means the scheduling system could not be reached or
	// failed in a way that may succeed later.
	ErrUnavailable = errors.New("scheduling system unavailable")

	// ErrOutcomeUnknown means a booking request was sent but its result was
	// never learned. The appointment may or may not exist, so the only safe
	// responses are to reconcile against the scheduling system and to say
	// nothing definite to the customer.
	ErrOutcomeUnknown = errors.New("booking outcome unknown")
)

// Service is something a customer can book.
type Service struct {
	ID       string
	Name     string
	Category string

	// Duration is how long the appointment takes.
	Duration time.Duration

	// PriceMin and PriceMax bound what the service costs, in the currency the
	// business is configured in. They are quoted to customers and never used
	// in arithmetic: this system does not take payments, and the scheduling
	// system remains the authority on what anything costs.
	PriceMin float64
	PriceMax float64
	Currency string
}

// PriceLabel renders the price the way it should be quoted to a customer.
func (s Service) PriceLabel() string {
	switch {
	case s.PriceMin == 0 && s.PriceMax == 0:
		return ""
	case s.PriceMin == s.PriceMax:
		return fmt.Sprintf("%.0f %s", s.PriceMin, s.Currency)
	default:
		return fmt.Sprintf("%.0f-%.0f %s", s.PriceMin, s.PriceMax, s.Currency)
	}
}

// Staff is a person who performs services.
type Staff struct {
	ID             string
	Name           string
	Specialisation string

	// Bookable reports whether this person currently accepts appointments.
	// Someone on leave still appears in the catalogue and must not be offered.
	Bookable bool
}

// Slot is a start time that was free when availability was read.
//
// A slot is a snapshot, not a hold. Nothing is reserved by reading it, so a
// booking against it can still be refused.
type Slot struct {
	Start    time.Time
	Duration time.Duration
	StaffID  string
}

// End returns when an appointment in this slot would finish.
func (s Slot) End() time.Time {
	return s.Start.Add(s.Duration)
}

// Status is where a booking has got to.
type Status string

const (
	// StatusConfirmed means the scheduling system has the appointment. It is
	// the only status a customer may be told about as a booking.
	StatusConfirmed Status = "confirmed"

	// StatusCancelled means the appointment was cancelled.
	StatusCancelled Status = "cancelled"
)

// Booking is an appointment that exists in the scheduling system.
//
// A value of this type is only ever created from a confirmed response. There is
// deliberately no "pending" status: a request that has not been confirmed is
// not a booking, and giving it a name here would invite telling a customer it
// exists.
type Booking struct {
	ID string

	// ExternalID is the scheduling system's own identifier, and the one to
	// quote when cancelling or rescheduling.
	ExternalID string

	// ManagementToken is the scheduling system's opaque proof that an online
	// booking may be managed by the customer who created it. It is persisted
	// for cancellation but must never be displayed to the customer or model.
	ManagementToken string

	CustomerID string
	ServiceIDs []string
	StaffID    string

	StartsAt time.Time
	Duration time.Duration
	Status   Status

	CreatedAt time.Time
}

// EndsAt returns when the appointment finishes.
func (b Booking) EndsAt() time.Time {
	return b.StartsAt.Add(b.Duration)
}

// Request is an appointment the application wants created.
type Request struct {
	// IdempotencyKey is this system's own identifier for the request, sent to
	// the scheduling system so that the same request submitted twice cannot
	// produce two appointments. It is generated once, when the customer
	// confirms, and reused unchanged on every attempt.
	IdempotencyKey string

	CustomerID   string
	CustomerName string
	Phone        string
	Email        string

	ServiceIDs []string
	StaffID    string
	StartsAt   time.Time
	Duration   time.Duration

	Comment string
}

// Selection is the part of an appointment needed to ask whether a time is
// available. Contact details and creation idempotency do not belong in a
// read-only availability check.
type Selection struct {
	ServiceIDs []string
	StaffID    string
	StartsAt   time.Time
	Duration   time.Duration
}

// Selection returns the schedulable part of this creation request.
func (r Request) Selection() Selection {
	return Selection{
		ServiceIDs: r.ServiceIDs,
		StaffID:    r.StaffID,
		StartsAt:   r.StartsAt,
		Duration:   r.Duration,
	}
}

// Validate reports whether the request is complete enough to send.
//
// The checks are here rather than in the adapter because they are business
// rules, not transport rules: they must hold no matter which scheduling system
// is behind the port, and they must not be skippable by anything a customer or
// a language model says.
func (r Request) Validate(now time.Time) error {
	switch {
	case r.IdempotencyKey == "":
		return fmt.Errorf("%w: idempotency key is empty", ErrRejected)
	case r.CustomerID == "":
		return fmt.Errorf("%w: customer is empty", ErrRejected)
	case r.Phone == "":
		return fmt.Errorf("%w: phone number is empty", ErrRejected)
	case len(r.ServiceIDs) == 0:
		return fmt.Errorf("%w: no service chosen", ErrRejected)
	case r.StaffID == "":
		return fmt.Errorf("%w: no staff member chosen", ErrRejected)
	case r.StartsAt.IsZero():
		return fmt.Errorf("%w: no start time chosen", ErrRejected)
	case r.Duration <= 0:
		return fmt.Errorf("%w: appointment duration is not positive", ErrRejected)
	case !r.StartsAt.After(now):
		return fmt.Errorf("%w: start time %s is not in the future", ErrRejected, r.StartsAt.Format(time.RFC3339))
	}
	return nil
}
