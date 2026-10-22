package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestOperatorPauseBlocksRepliesAndTemplatesDespiteGreenCloudHealth(t *testing.T) {
	repo := &testRepository{rows: map[string]Entry{}}
	reads := 0
	guard := &HealthGuard{Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
		reads++
		return messaging.ChannelHealth{Known: true}, nil
	}}
	repo.rows[guard.key()] = Entry{Kind: "channel_health", OperatorPaused: true, OperatorPauseReason: "phone app review pending", Health: messaging.ChannelHealth{Known: true}}
	for _, template := range []string{"", "approved_booking"} {
		if allowed, err := guard.Allows(t.Context(), template, "en"); err != nil || allowed {
			t.Fatalf("operator pause bypassed: allowed=%v err=%v", allowed, err)
		}
	}
	if err := guard.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || !repo.rows[guard.key()].OperatorPaused {
		t.Fatal("periodic probe changed a manual review hold")
	}
}

func TestOperatorPauseWinsAgainstProbeAlreadyInFlight(t *testing.T) {
	repo := &testRepository{rows: map[string]Entry{}}
	var guard *HealthGuard
	guard = &HealthGuard{Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
		if err := repo.TransactNotifications(t.Context(), []string{guard.key()}, func(rows map[string]*Entry) error {
			rows[guard.key()].OperatorPaused = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return messaging.ChannelHealth{Known: true}, nil
	}}
	if allowed, err := guard.Allows(t.Context(), "", ""); err != nil || allowed {
		t.Fatal("in-flight green probe reopened operator-paused WhatsApp")
	}
}

func TestOperatorPauseIsProviderScopedAndHasNoAutomaticExpiry(t *testing.T) {
	s, repo, _, _ := notificationFixture(t)
	now := s.now()
	wa := &HealthGuard{Repo: repo, Now: func() time.Time { return now }}
	repo.rows[wa.key()] = Entry{Kind: "channel_health", OperatorPaused: true, NextLookupAt: now.Add(time.Hour)}
	now = now.Add(48 * time.Hour)
	if allowed, err := wa.Allows(t.Context(), "", ""); err != nil || allowed {
		t.Fatal("review hold expired automatically")
	}
	instagram := &HealthGuard{Provider: messaging.ProviderInstagram, Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
		return messaging.ChannelHealth{Known: true}, nil
	}}
	if allowed, err := instagram.Allows(t.Context(), "", ""); err != nil || !allowed {
		t.Fatal("WhatsApp review hold disabled Instagram")
	}
}

func TestBookingRequestDoesNotEnrollCustomerIntoWhatsApp(t *testing.T) {
	for _, source := range []string{"manual", "native online", "owned web", "staff request"} {
		t.Run(source, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.SMSReady = true
			s.ManualPermissionSince, s.BookingPermissionSince, s.SMSPermissionSince = s.now().Add(-time.Minute), s.now().Add(-time.Minute), s.now().Add(-time.Minute)
			switch source {
			case "native online":
				reader.snapshot.Online = true
			case "owned web":
				reader.snapshot.APIID = "emotion-web-v2-test"
				repo.rows[Key("web_phone", reader.snapshot.Phone)] = Entry{Contact: Contact{Phone: reader.snapshot.Phone, ExpiresAt: s.now().Add(time.Hour)}}
			case "staff request":
				if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
					t.Fatal(err)
				}
			}
			ingestAndDeliver(t, s, "create")
			if len(sender.channels) != 1 || sender.channels[0] != SMS {
				t.Fatalf("generic %s request implied WhatsApp opt-in: %v", source, sender.channels)
			}
		})
	}
}

func TestOnlyCurrentExactWhatsAppOptInEnablesProactiveMessages(t *testing.T) {
	for _, mode := range []string{"active", "expired", "wrong phone", "withdrawn"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			repo.rows = map[string]Entry{}
			s.SMSReady = true
			s.ManualPermissionSince = s.now().Add(-time.Minute)
			if err := s.LinkWhatsApp(t.Context(), reader.snapshot.Phone, "en", true); err != nil {
				t.Fatal(err)
			}
			key := Key("whatsapp_phone", reader.snapshot.Phone)
			row := repo.rows[key]
			switch mode {
			case "expired":
				row.Contact.ExpiresAt = s.now().Add(-time.Second)
			case "wrong phone":
				row.Contact.Phone = "+37499123456"
			case "withdrawn":
				row.Contact.Blocked = true
			}
			repo.rows[key] = row
			ingestAndDeliver(t, s, "create")
			want := SMS
			if mode == "active" {
				want = WhatsApp
			}
			if len(sender.channels) != 1 || sender.channels[0] != want {
				t.Fatalf("%s opt-in routed %v", mode, sender.channels)
			}
		})
	}
}

func TestGenericStaffRequestCannotUndoWhatsAppWithdrawal(t *testing.T) {
	s, repo, reader, _ := notificationFixture(t)
	if err := s.LinkWhatsApp(t.Context(), reader.snapshot.Phone, "en", false); err != nil {
		t.Fatal(err)
	}
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	if !repo.rows[Key("whatsapp_phone", reader.snapshot.Phone)].Contact.Blocked {
		t.Fatal("generic staff request cleared a WhatsApp-specific withdrawal")
	}
}
