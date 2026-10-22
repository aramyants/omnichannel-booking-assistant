package notifications

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/google/uuid"
)

type unavailableRecord struct{ err error }

func (r unavailableRecord) ReadNativeBooking(context.Context, string) (Snapshot, error) {
	return Snapshot{}, r.err
}

func TestMissingNativeRecordTerminatesWithoutSendingOrRetrying(t *testing.T) {
	for _, state := range []string{"pending", "prepared"} {
		t.Run(state, func(t *testing.T) {
			s, repo, _, sender := notificationFixture(t)
			id := uuid.NewString()
			repo.rows[Key("event", id)] = Entry{Kind: "event", State: state, Event: Event{ID: id, RecordID: "42"}}
			s.Reader = unavailableRecord{fmt.Errorf("read: %w", booking.ErrNotFound)}
			if err := s.Deliver(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			row := repo.rows[Key("event", id)]
			if row.State != "done" || row.Outcome != "record_unavailable" || len(sender.channels) != 0 {
				t.Fatalf("missing record not retired safely: %+v", row)
			}
			if err := s.Deliver(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnavailableNativeRecordRemainsRetryable(t *testing.T) {
	s, repo, _, sender := notificationFixture(t)
	id := uuid.NewString()
	repo.rows[Key("event", id)] = Entry{Kind: "event", State: "pending", Event: Event{ID: id, RecordID: "42"}}
	s.Reader = unavailableRecord{booking.ErrUnavailable}
	if err := s.Deliver(t.Context(), id); err == nil {
		t.Fatal("temporary outage became success")
	}
	if repo.rows[Key("event", id)].State != "pending" || len(sender.channels) != 0 {
		t.Fatal("temporary outage lost recoverable work")
	}
}

type taskIdentityScheduler struct{ ids []string }

func (s *taskIdentityScheduler) ScheduleNotification(_ context.Context, id, _ string, _ time.Time) error {
	s.ids = append(s.ids, id)
	return nil
}
func TestRecoveryReusesTaskIdentityAcrossFiveMinuteTicks(t *testing.T) {
	s, repo, _, _ := notificationFixture(t)
	now := s.now().Truncate(time.Hour).Add(time.Minute)
	s.Now = func() time.Time { return now }
	scheduler := &taskIdentityScheduler{}
	s.Scheduler = scheduler
	id := uuid.NewString()
	repo.rows[Key("event", id)] = Entry{Kind: "event", State: "pending", Event: Event{ID: id, RecordID: "42"}}
	for range 3 {
		if err := s.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(5 * time.Minute)
	}
	if len(scheduler.ids) != 3 || scheduler.ids[0] != scheduler.ids[1] || scheduler.ids[0] != scheduler.ids[2] {
		t.Fatalf("recovery multiplied tasks: %v", scheduler.ids)
	}
}
