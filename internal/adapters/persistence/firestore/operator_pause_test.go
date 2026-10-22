package firestore

import (
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestOperatorReviewHoldSurvivesFirestoreTransaction(t *testing.T) {
	store := newStore(t)
	key := notifications.Key("channel_health", "whatsapp")
	if err := store.TransactNotifications(t.Context(), []string{key}, func(rows map[string]*notifications.Entry) error {
		*rows[key] = notifications.Entry{Kind: "channel_health", OperatorPaused: true, OperatorPauseReason: "phone ban review pending", Health: messaging.ChannelHealth{Known: true, Blocked: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.TransactNotifications(t.Context(), []string{key}, func(rows map[string]*notifications.Entry) error {
		rows[key].NextLookupAt = testNow
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetNotification(t.Context(), key)
	if err != nil || !row.OperatorPaused || row.OperatorPauseReason != "phone ban review pending" {
		t.Fatalf("durable operator hold lost: paused=%v reason=%q err=%v", row.OperatorPaused, row.OperatorPauseReason, err)
	}
}
