package notifications

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (r *testRepository) PendingSMSNotifications(context.Context) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []Entry
	for _, row := range r.rows {
		if awaitingSMS(row) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt.Before(rows[j].UpdatedAt) })
	if len(rows) > 20 {
		rows = rows[:20]
	}
	return rows, nil
}

func smsReceiptFixture(t *testing.T) (*Service, *testRepository, string) {
	t.Helper()
	s, repo, reader, _ := notificationFixture(t)
	id := uuid.NewString()
	repo.rows[Key("event", id)] = Entry{Kind: "event", State: "done", Outcome: "accepted_sms",
		Event:  Event{ID: id, RecordID: "42", ReceivedAt: s.now()},
		Notice: Notice{ID: id, Snapshot: reader.snapshot}, UpdatedAt: s.now()}
	return s, repo, id
}

func TestSMSReceiptDoesNotTreatQueuedOrSentAsDelivered(t *testing.T) {
	for _, state := range []string{"Pending", "Processed", "Sent", "Cancelling", "Delivered"} {
		t.Run(state, func(t *testing.T) {
			s, repo, id := smsReceiptFixture(t)
			calls := 0
			s.SMSStatus = func(_ context.Context, notice Notice) (string, error) {
				calls++
				if notice.ID != id {
					t.Fatal("queried another message")
				}
				return state, nil
			}
			for range 2 {
				if err := s.Reconcile(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			row := repo.rows[Key("event", id)]
			if calls != 1 || row.Outcome != "accepted_sms" || row.LeaseOwner != "" || !row.NextLookupAt.After(s.now()) {
				t.Fatalf("queue acceptance was promoted or check unpaced: calls=%d row=%+v", calls, row)
			}
		})
	}
}

func TestSMSFailedReceiptAlertsOnceAndNeverResends(t *testing.T) {
	s, repo, id := smsReceiptFixture(t)
	scheduler := &recoveryScheduler{}
	s.Scheduler = scheduler
	s.SMSStatus = func(context.Context, Notice) (string, error) { return "Failed", nil }
	alerts := 0
	s.SMSFailureAlert = func(_ context.Context, recordID, eventID string) error {
		alerts++
		if recordID != "42" || eventID != id || repo.rows[Key("event", id)].SMSFailureAlertedAt.IsZero() {
			t.Fatal("alert was not durably reserved for the failed message")
		}
		return errors.New("alert acceptance unknown")
	}
	if err := s.Reconcile(t.Context()); err == nil {
		t.Fatal("lost alert failure")
	}
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	row := repo.rows[Key("event", id)]
	if row.Outcome != "failed_sms" || row.ProviderState != "sms_failed" || alerts != 1 || len(scheduler.events) != 0 {
		t.Fatalf("confirmed failure lost or resent: row=%+v alerts=%d tasks=%v", row, alerts, scheduler.events)
	}
}

func TestSMSDeliveredPartCanLaterFail(t *testing.T) {
	s, repo, id := smsReceiptFixture(t)
	state := "Delivered"
	s.SMSStatus = func(context.Context, Notice) (string, error) { return state, nil }
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	state = "Failed"
	at := s.now().Add(5 * time.Minute)
	s.Now = func() time.Time { return at }
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repo.rows[Key("event", id)].Outcome != "failed_sms" {
		t.Fatal("partial delivery hid a subsequent failed SMS part")
	}
}

func TestSMSStatusUnavailablePreservesUncertainAcceptance(t *testing.T) {
	s, repo, id := smsReceiptFixture(t)
	row := repo.rows[Key("event", id)]
	row.Outcome = "uncertain_sms"
	repo.rows[Key("event", id)] = row
	readErr := errors.New("status unavailable or message missing")
	s.SMSStatus = func(context.Context, Notice) (string, error) { return "", readErr }
	if err := s.Reconcile(t.Context()); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	row = repo.rows[Key("event", id)]
	if row.Outcome != "uncertain_sms" || row.LeaseOwner != "" || !row.NextLookupAt.After(s.now()) {
		t.Fatal("unavailable status guessed a delivery result or left the lease")
	}
}

func TestSMSHistoricalFailureIsCheckedBeforeObservationWindowEnds(t *testing.T) {
	s, repo, id := smsReceiptFixture(t)
	row := repo.rows[Key("event", id)]
	row.Event.ReceivedAt = s.now().Add(-48 * time.Hour)
	repo.rows[Key("event", id)] = row
	s.SMSStatus = func(context.Context, Notice) (string, error) { return "Failed", nil }
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repo.rows[Key("event", id)].Outcome != "failed_sms" {
		t.Fatal("historical accepted SMS was silently dropped before checking provider")
	}
}

func TestSMSReadLeasePreventsConcurrentChecks(t *testing.T) {
	s, _, _ := smsReceiptFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s.SMSStatus = func(context.Context, Notice) (string, error) {
		close(entered)
		<-release
		return "Pending", nil
	}
	done := make(chan error, 1)
	go func() { done <- s.Reconcile(t.Context()) }()
	<-entered
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
