package notifications

import (
	"context"
	"errors"
	"testing"
	"time"
)

type changeReader struct {
	*testReader
	from, to time.Time
	err      error
}

func (r *changeReader) ListChangedNativeBookings(_ context.Context, from, to time.Time) ([]string, error) {
	r.from, r.to = from, to
	return []string{"42"}, r.err
}
func (r *testRepository) UnroutedNotifications(_ context.Context) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Entry
	for _, row := range r.rows {
		if row.Outcome == "no_linked_messenger" {
			out = append(out, row)
		}
	}
	return out, nil
}
func TestCalendarDiscoveryRecoversMissingWebhookWithoutDuplicateSend(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	reader.snapshot.ChangedAt = s.now().Add(-time.Minute)
	reader.snapshot.Booking.CreatedAt = reader.snapshot.ChangedAt
	s.Reader = &changeReader{testReader: reader}
	scheduler := &recoveryScheduler{}
	s.Scheduler = scheduler
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(scheduler.events) == 0 {
		t.Fatal("missing calendar event")
	}
	event := scheduler.events[0]
	if err := s.Deliver(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	ingestAndDeliver(t, s, "create")
	if len(sender.channels) != 1 {
		t.Fatal("poll and webhook duplicated", sender.channels)
	}
	if repo.rows["calendar_reconciliation_v1"].ChangedAt.IsZero() {
		t.Fatal("cursor not committed")
	}
}
func TestCalendarFailurePreservesCursorAndUnrelatedBacklog(t *testing.T) {
	s, repo, reader, _ := notificationFixture(t)
	before := s.now().Add(-time.Hour)
	s.ActivatedAt = s.now().Add(-24 * time.Hour)
	repo.rows["calendar_reconciliation_v1"] = Entry{ChangedAt: before}
	expected := errors.New("provider down")
	r := &changeReader{testReader: reader, err: expected}
	s.Reader = r
	if err := s.Reconcile(t.Context()); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	if !repo.rows["calendar_reconciliation_v1"].ChangedAt.Equal(before) || !r.from.Equal(before.Add(-10*time.Minute)) {
		t.Fatal("cursor advanced after failure")
	}
}
func TestOnlyProvenUnsentManualNoticeRecoversAfterSourceRepair(t *testing.T) {
	for _, outcome := range []string{"no_linked_messenger", "accepted_telegram", "uncertain_sms"} {
		t.Run(outcome, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.SMSReady = true
			s.TelegramAccount = true
			id := ingestAndDeliver(t, s, "create")
			if len(sender.channels) != 0 {
				t.Fatal("setup")
			}
			s.ManualPermissionSince = s.now().Add(-time.Hour)
			sender.channels = nil
			row := repo.rows[Key("event", id)]
			row.Outcome = outcome
			row.Attempted = nil
			repo.rows[Key("event", id)] = row
			rec := repo.rows[Key("record", "42")]
			rec.Contact = Contact{}
			repo.rows[Key("record", "42")] = rec
			scheduler := &recoveryScheduler{}
			s.Scheduler = scheduler
			if err := s.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if outcome == "no_linked_messenger" {
				if err := s.Deliver(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				if len(sender.channels) != 1 {
					t.Fatal("unsent notice not repaired")
				}
				if repo.rows[Key("record", "42")].Contact.Phone != reader.snapshot.Phone {
					t.Fatal("missing exact recipient permission")
				}
			} else if len(scheduler.events) != 0 {
				t.Fatal("accepted or ambiguous send reopened")
			}
		})
	}
}
