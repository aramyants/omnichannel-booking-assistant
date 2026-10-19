package notifications

import (
	"context"
	"testing"
	"time"
)

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
