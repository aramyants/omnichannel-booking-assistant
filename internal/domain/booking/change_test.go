package booking

import (
	"errors"
	"testing"
	"time"
)

func validChange(kind ChangeKind) ChangeDraft {
	return ChangeDraft{
		Kind:                  kind,
		Reference:             "998877",
		NewStart:              now.Add(48 * time.Hour),
		PreparedAt:            now,
		PreparedFromMessageID: "message-1",
	}
}

func TestChangeDraftValidate(t *testing.T) {
	for _, kind := range []ChangeKind{ChangeCancel, ChangeReschedule} {
		if err := validChange(kind).Validate(kind, now); err != nil {
			t.Errorf("valid %s change was rejected: %v", kind, err)
		}
	}

	tests := map[string]func(*ChangeDraft){
		"wrong operation":     func(d *ChangeDraft) { d.Kind = ChangeCancel },
		"no reference":        func(d *ChangeDraft) { d.Reference = "" },
		"no preparation time": func(d *ChangeDraft) { d.PreparedAt = time.Time{} },
		"no source message":   func(d *ChangeDraft) { d.PreparedFromMessageID = "" },
		"expired":             func(d *ChangeDraft) { d.PreparedAt = now.Add(-2 * time.Hour) },
		"new time has passed": func(d *ChangeDraft) { d.NewStart = now.Add(-time.Minute) },
	}
	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			draft := validChange(ChangeReschedule)
			breakIt(&draft)
			if err := draft.Validate(ChangeReschedule, now); !errors.Is(err, ErrRejected) {
				t.Errorf("Validate() = %v, want ErrRejected", err)
			}
		})
	}
}
