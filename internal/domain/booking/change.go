package booking

import (
	"fmt"
	"time"
)

// maxChangeDraftAge limits how long a cancellation or reschedule confirmation
// remains valid. A short-lived draft prevents a much later "yes" from changing
// an appointment whose circumstances may have changed in the meantime.
const maxChangeDraftAge = time.Hour

// ChangeKind identifies the one mutation a customer is being asked to confirm.
type ChangeKind string

const (
	ChangeCancel     ChangeKind = "cancel"
	ChangeReschedule ChangeKind = "reschedule"
)

// ChangeDraft is an appointment change that has been described to the customer
// but not applied. Confirmation tools take no arguments and consume this value,
// so a model cannot swap the reference or new time after the customer agrees.
type ChangeDraft struct {
	Kind       ChangeKind
	Reference  string
	NewStart   time.Time
	PreparedAt time.Time

	// PreparedFromMessageID ensures the change cannot be prepared and applied
	// during one model turn before the customer has seen its summary.
	PreparedFromMessageID string
}

// Validate reports whether this draft can still be confirmed as expected.
func (d ChangeDraft) Validate(expected ChangeKind, now time.Time) error {
	switch {
	case d.Kind != expected:
		return fmt.Errorf("%w: prepared change is %q, not %q", ErrRejected, d.Kind, expected)
	case d.Reference == "":
		return fmt.Errorf("%w: prepared change has no appointment reference", ErrRejected)
	case d.PreparedAt.IsZero():
		return fmt.Errorf("%w: prepared change has no preparation time", ErrRejected)
	case d.PreparedFromMessageID == "":
		return fmt.Errorf("%w: prepared change is not tied to a customer message", ErrRejected)
	case now.Sub(d.PreparedAt) > maxChangeDraftAge:
		return fmt.Errorf("%w: prepared change is more than %s old", ErrRejected, maxChangeDraftAge)
	case expected == ChangeReschedule && !d.NewStart.After(now):
		return fmt.Errorf("%w: new appointment time has passed", ErrRejected)
	}
	return nil
}
