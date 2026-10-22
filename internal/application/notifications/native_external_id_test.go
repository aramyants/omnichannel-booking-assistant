package notifications

import (
	"reflect"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/google/uuid"
)

func TestNativeExternalIDDoesNotSuppressManualBookingLifecycle(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	repo.rows = map[string]Entry{}
	s.ManualPermissionSince = s.now().Add(-time.Minute)
	s.SMSReady = true
	reader.snapshot.APIID = "a1b85ccc-11e3-4f08-92e5-9a0c552a1b84"
	ingestAndDeliver(t, s, "create")
	if !reflect.DeepEqual(sender.channels, []Channel{WhatsApp}) {
		t.Fatalf("manual confirmation with external ID did not reach WhatsApp: %v", sender.channels)
	}
	reader.snapshot.Booking.StartsAt = reader.snapshot.Booking.StartsAt.Add(time.Hour)
	reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
	id := ingestAndDeliver(t, s, "update")
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	reader.snapshot.Booking.Status = booking.StatusCancelled
	reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
	ingestAndDeliver(t, s, "delete")
	if !reflect.DeepEqual(sender.channels, []Channel{WhatsApp, WhatsApp, WhatsApp}) {
		t.Fatalf("manual update/cancellation/dedup routes: %v", sender.channels)
	}
}

func TestNativeExternalIDKeepsCutoverAndRecipientProtections(t *testing.T) {
	for _, mode := range []string{"before cutover", "disabled", "withdrawn", "recipient changed", "web unconfirmed", "assistant owned"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.ManualPermissionSince = s.now().Add(-time.Minute)
			s.SMSReady = true
			reader.snapshot.APIID = "a1b85ccc-11e3-4f08-92e5-9a0c552a1b84"
			switch mode {
			case "before cutover":
				reader.snapshot.Booking.CreatedAt = s.now().Add(-time.Hour)
			case "disabled":
				s.ManualPermissionSince = time.Time{}
			case "withdrawn":
				if err := s.BlockPhone(t.Context(), reader.snapshot.Phone); err != nil {
					t.Fatal(err)
				}
			case "web unconfirmed":
				reader.snapshot.APIID = "emotion-web-v2-test"
			case "assistant owned":
				reader.snapshot.APIID = "omnichannel-test"
			case "recipient changed":
				ingestAndDeliver(t, s, "create")
				sender.channels = nil
				reader.snapshot.Phone = "+37499123456"
				reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
			}
			ingestAndDeliver(t, s, "update")
			if len(sender.channels) != 0 {
				t.Fatalf("%s produced an unauthorized send: %v", mode, sender.channels)
			}
		})
	}
}

func TestUnsentManualExternalIDRecoversWithoutReplayingAcceptedSends(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	repo.rows = map[string]Entry{}
	reader.snapshot.APIID = "a1b85ccc-11e3-4f08-92e5-9a0c552a1b84"
	id := ingestAndDeliver(t, s, "create")
	if len(sender.channels) != 0 {
		t.Fatal("fixture sent before manual notices were enabled")
	}
	s.ManualPermissionSince = s.now().Add(-time.Minute)
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.channels, []Channel{WhatsApp}) {
		t.Fatalf("proven-unsent external-ID appointment was not recovered: %v", sender.channels)
	}
	if err := s.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 1 {
		t.Fatal("accepted confirmation was resent")
	}
	// A distinct event with the exact same native state must also be a no-op.
	duplicate := Event{ID: uuid.NewString(), RecordID: "42", Status: "create"}
	if err := s.Ingest(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(t.Context(), duplicate.ID); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 1 {
		t.Fatal("native duplicate event caused another send")
	}
}

func TestManualExternalIDFallsBackOnlyAfterDefiniteRejection(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "uncertain"}[uncertain], func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.ManualPermissionSince = s.now().Add(-time.Minute)
			s.TelegramAccount, s.SMSReady = true, true
			reader.snapshot.APIID = "a1b85ccc-11e3-4f08-92e5-9a0c552a1b84"
			sender.reject = true
			sender.rejectWhatsApp = !uncertain
			if uncertain {
				sender.uncertainChannel = WhatsApp
			}
			id := ingestAndDeliver(t, s, "create")
			for range 2 {
				if err := s.Deliver(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			want := []Channel{Telegram, WhatsApp, SMS}
			if uncertain {
				want = []Channel{Telegram, WhatsApp}
			}
			if !reflect.DeepEqual(sender.channels, want) {
				t.Fatalf("unsafe or missing fallback: got %v want %v", sender.channels, want)
			}
		})
	}
}
