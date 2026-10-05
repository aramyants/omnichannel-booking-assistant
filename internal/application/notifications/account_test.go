package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStudioAccountPlanKeepsBotIdentityAndWithdrawal(t *testing.T) {
	policy := Policy{TelegramAccount: true, Enabled: map[Channel]bool{Telegram: true, WhatsApp: true}, Templates: map[Purpose]string{BookingCreated: "approved"}}
	for _, tc := range []struct {
		name      string
		recipient Recipient
		want      int
		account   bool
	}{
		{"unlinked requesting client", Recipient{Phone: "+37491123456", BookingUpdatesRequested: true}, 1, true},
		{"number alone", Recipient{Phone: "+37491123456"}, 0, false},
		{"linked client keeps bot", Recipient{Phone: "+37491123456", TelegramChat: "123", TelegramPhone: "+37491123456", BookingUpdatesRequested: true}, 1, false},
		{"withdrawal overrides new booking", Recipient{Phone: "+37491123456", TelegramOptedOut: true, BookingUpdatesRequested: true}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Plan(policy, tc.recipient, BookingCreated)
			if err != nil || len(plan) != tc.want {
				t.Fatalf("unexpected plan: %v %v", plan, err)
			}
			if len(plan) > 0 && plan[0].StudioAccount != tc.account {
				t.Fatal("wrong Telegram sender identity")
			}
		})
	}
}

func TestOnlineRequestDoesNotApplyToAdministratorOrChangedPhone(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		online       bool
		api          string
		before       bool
		want         int
	}{
		{"new online booking", "create", true, "", false, 1},
		{"administrator booking", "create", false, "", false, 0},
		{"external API booking", "create", true, "external-app", false, 0},
		{"before visible permission", "create", true, "", true, 0},
		{"lost creation event", "update", true, "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.TelegramAccount = true
			s.BookingPermissionSince = s.now().Add(-time.Second)
			reader.snapshot.Online = tc.online
			reader.snapshot.APIID = tc.api
			if tc.before {
				s.BookingPermissionSince = s.now().Add(time.Second)
			}
			id := uuid.NewString()
			if err := s.Ingest(context.Background(), Event{ID: id, RecordID: "42", Status: tc.status}); err != nil {
				t.Fatal(err)
			}
			if err := s.Deliver(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if len(sender.channels) != tc.want {
				t.Fatalf("sent %d notices, want %d", len(sender.channels), tc.want)
			}
			if tc.want == 1 {
				reader.snapshot.Phone = "+37491234567"
				reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
				next := uuid.NewString()
				_ = s.Ingest(context.Background(), Event{ID: next, RecordID: "42", Status: "update"})
				if err := s.Deliver(context.Background(), next); err != nil {
					t.Fatal(err)
				}
				if len(sender.channels) != 1 {
					t.Fatal("booking request silently transferred to another phone")
				}
			}
		})
	}
}

func TestWhatsAppStopIsDurableAfterAnotherOnlineBooking(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	repo.rows = map[string]Entry{}
	s.BookingPermissionSince = s.now().Add(-time.Second)
	reader.snapshot.Online = true
	if err := s.LinkWhatsApp(context.Background(), reader.snapshot.Phone, "en", false); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_ = s.Ingest(context.Background(), Event{ID: id, RecordID: "42", Status: "create"})
	if err := s.Deliver(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 0 {
		t.Fatal("online booking overrode WhatsApp withdrawal")
	}
}

func TestStaffRequestAndGlobalWithdrawalApplyToAdminBooking(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	repo.rows = map[string]Entry{}
	s.TelegramAccount = true
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "hy"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_ = s.Ingest(t.Context(), Event{ID: id, RecordID: "42", Status: "create"})
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 1 || sender.channels[0] != Telegram || sender.languages[0] != "hy" {
		t.Fatal("documented administrator booking request did not route in the saved language")
	}
	if err := s.BlockPhone(t.Context(), reader.snapshot.Phone); err != nil {
		t.Fatal(err)
	}
	reader.snapshot.Booking.StartsAt = reader.snapshot.Booking.StartsAt.Add(time.Hour)
	reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
	next := uuid.NewString()
	_ = s.Ingest(t.Context(), Event{ID: next, RecordID: "42", Status: "update"})
	if err := s.Deliver(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 1 {
		t.Fatal("withdrawal did not suppress both account and WhatsApp fallback")
	}
}
