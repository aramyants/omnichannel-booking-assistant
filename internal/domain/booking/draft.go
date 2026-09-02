package booking

import (
	"fmt"
	"time"
)

// maxDraftAge is how long a prepared booking stays confirmable.
//
// A draft is not a hold. After an hour the availability it was built from is
// stale enough that confirming it is more likely to fail than succeed, and
// asking the customer to pick again is better than telling them a time is
// theirs when it is not.
const maxDraftAge = time.Hour

// Draft is a booking a customer has been shown but has not yet confirmed.
//
// It exists so that agreeing to a booking and making one are two separate
// steps. The details are fixed when the draft is prepared and are not
// re-read at confirmation, so what the customer agreed to is what gets booked.
type Draft struct {
	// IdempotencyKey is generated once, when the draft is prepared, and reused
	// unchanged on every confirmation attempt. Regenerating it would turn a
	// retry into a second appointment.
	IdempotencyKey string

	ServiceIDs   []string
	ServiceNames []string
	StaffID      string
	StaffName    string
	StartsAt     time.Time
	Duration     time.Duration

	// Phone is how the business reaches the customer. Messaging providers do
	// not reliably supply it, so it is asked for and stored here.
	Phone        string
	CustomerName string

	PreparedAt time.Time
}

// Expired reports whether the draft is too old to confirm.
func (d Draft) Expired(now time.Time) bool {
	return now.Sub(d.PreparedAt) > maxDraftAge
}

// EndsAt returns when the appointment would finish.
func (d Draft) EndsAt() time.Time {
	return d.StartsAt.Add(d.Duration)
}

// Validate reports whether the draft is complete enough to confirm.
func (d Draft) Validate(now time.Time) error {
	switch {
	case d.IdempotencyKey == "":
		return fmt.Errorf("%w: the draft has no idempotency key", ErrRejected)
	case len(d.ServiceIDs) == 0:
		return fmt.Errorf("%w: the draft names no service", ErrRejected)
	case d.StaffID == "":
		return fmt.Errorf("%w: the draft names no specialist", ErrRejected)
	case d.Phone == "":
		return fmt.Errorf("%w: the draft has no phone number", ErrRejected)
	case d.StartsAt.IsZero():
		return fmt.Errorf("%w: the draft has no start time", ErrRejected)
	case d.Expired(now):
		return fmt.Errorf("%w: the draft was prepared more than %s ago", ErrRejected, maxDraftAge)
	case !d.StartsAt.After(now):
		return fmt.Errorf("%w: the appointment time has passed", ErrRejected)
	}
	return nil
}

// ToRequest turns a confirmed draft into a booking request.
func (d Draft) ToRequest(customerID string) Request {
	return Request{
		IdempotencyKey: d.IdempotencyKey,
		CustomerID:     customerID,
		CustomerName:   d.CustomerName,
		Phone:          d.Phone,
		ServiceIDs:     d.ServiceIDs,
		StaffID:        d.StaffID,
		StartsAt:       d.StartsAt,
	}
}
