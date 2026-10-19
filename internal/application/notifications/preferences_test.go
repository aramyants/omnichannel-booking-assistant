package notifications

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestVerifiedLanguageChoiceWinsAcrossRoutesAndDeviceHints(t *testing.T) {
	s, _, r, sender := notificationFixture(t)
	ctx := context.Background()
	phone := r.snapshot.Phone
	if lang, err := s.ResolveLanguage(ctx, phone, "en", true); err != nil || lang != "en" {
		t.Fatal(lang, err)
	}
	if lang, err := s.ResolveLanguage(ctx, phone, "hy", false); err != nil || lang != "en" {
		t.Fatal(lang, err)
	}
	s.SMSReady = true
	s.SMSPermissionSince = s.now().Add(-time.Hour)
	s.BookingPermissionSince = s.SMSPermissionSince
	r.snapshot.Online = true
	sender.reject = true
	ingestAndDeliver(t, s, "create")
	for _, lang := range sender.languages {
		if lang != "en" {
			t.Fatalf("fallback changed language: %v", sender.languages)
		}
	}
	if lang, err := s.ResolveLanguage(ctx, phone, "ru", true); err != nil || lang != "ru" {
		t.Fatal(lang, err)
	}
	if _, err := s.ResolveLanguage(ctx, phone, "de", true); err == nil {
		t.Fatal("unsupported language accepted")
	}
}
func TestAdminNoticeCutoverWithdrawalAndRecipientRotation(t *testing.T) {
	for _, mode := range []string{"new", "historical", "withdrawn"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, r, sender := notificationFixture(t)
			ctx := context.Background()
			repo.rows = map[string]Entry{}
			s.TelegramAccount = false
			s.SMSReady = true
			s.ManualPermissionSince = s.now().Add(-time.Minute)
			if mode == "historical" {
				r.snapshot.Booking.CreatedAt = s.now().Add(-time.Hour)
			}
			if mode == "withdrawn" {
				if err := s.BlockPhone(ctx, r.snapshot.Phone); err != nil {
					t.Fatal(err)
				}
			}
			ingestAndDeliver(t, s, "create")
			want := 0
			if mode == "new" {
				want = 1
			}
			if len(sender.channels) != want {
				t.Fatal(mode, sender.channels)
			}
			if want == 1 {
				before := repo.rows[Key("record", "42")].CalendarID
				if before == "" {
					t.Fatal("no private calendar identity")
				}
				r.snapshot.Phone = "+37499123456"
				r.snapshot.ChangedAt = s.now().Add(time.Second)
				e := Event{ID: uuid.NewString(), RecordID: "42", Status: "update"}
				s.Ingest(ctx, e)
				s.Deliver(ctx, e.ID)
				if repo.rows[Key("record", "42")].CalendarID == before {
					t.Fatal("recipient change retained calendar access")
				}
				if len(sender.channels) != 1 {
					t.Fatal("permission transferred to another phone")
				}
			}
		})
	}
}
