package firestore

import (
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
)

func TestSMSReceiptBacklogIncludesHistoricalAcceptanceAndExcludesTerminal(t *testing.T) {
	s := newStore(t)
	ctx := opCtx(t)
	id := unique(t, "sms-receipt")
	key := notifications.Key("event", id)
	if err := s.TransactNotifications(ctx, []string{key}, func(rows map[string]*notifications.Entry) error {
		*rows[key] = notifications.Entry{Kind: "event", State: "done", Outcome: "accepted_sms",
			Event: notifications.Event{ID: id}, UpdatedAt: testNow,
			SMSFailureAlertedAt: time.Time{}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	find := func() bool {
		t.Helper()
		rows, err := s.PendingSMSNotifications(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Event.ID == id {
				return true
			}
		}
		return false
	}
	if !find() {
		t.Fatal("historical accepted SMS missing from receipt query")
	}
	if err := s.TransactNotifications(ctx, []string{key}, func(rows map[string]*notifications.Entry) error {
		rows[key].Outcome, rows[key].SMSFailureAlertedAt = "failed_sms", testNow
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if find() {
		t.Fatal("terminal SMS still occupies receipt backlog")
	}
	row, err := s.GetNotification(ctx, key)
	if err != nil || !row.SMSFailureAlertedAt.Equal(testNow) {
		t.Fatalf("alert reservation did not persist: time=%v err=%v", row.SMSFailureAlertedAt, err)
	}
}
