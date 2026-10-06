package notifications

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestSMSRequiresCommissionedPhoneAndCurrentBookingRequest(t *testing.T) {
	for _, name := range []string{"eligible", "disabled", "no request", "before disclosure", "withdrawn", "changed phone"} {
		t.Run(name, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			s.SMSReady = true
			s.SMSPermissionSince = s.now().Add(-time.Minute)
			s.BookingPermissionSince = s.SMSPermissionSince
			reader.snapshot.Online = true
			s.WhatsAppTemplates = nil
			delete(repo.rows, Key("telegram_phone", reader.snapshot.Phone))
			delete(repo.rows, Key("whatsapp_phone", reader.snapshot.Phone))
			switch name {
			case "disabled":
				s.SMSReady = false
			case "no request":
				reader.snapshot.Online = false
			case "before disclosure":
				s.SMSPermissionSince = s.now().Add(time.Minute)
			case "withdrawn":
				if err := s.BlockPhone(context.Background(), reader.snapshot.Phone); err != nil {
					t.Fatal(err)
				}
			case "changed phone":
				ingestAndDeliver(t, s, "create")
				sender.channels = nil
				reader.snapshot.Phone = "+37491234567"
				reader.snapshot.ChangedAt = s.now().Add(time.Minute)
			}
			status := "create"
			if name == "changed phone" {
				status = "update"
			}
			ingestAndDeliver(t, s, status)
			if name == "eligible" {
				if !reflect.DeepEqual(sender.channels, []Channel{SMS}) {
					t.Fatalf("routes: %v", sender.channels)
				}
			} else if len(sender.channels) != 0 {
				t.Fatalf("unrequested SMS: %v", sender.channels)
			}
		})
	}
}

func TestSMSIsLastAndNeverFollowsUncertainMessengerAcceptance(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		s, _, reader, sender := notificationFixture(t)
		s.SMSReady = true
		if err := s.AllowPhone(context.Background(), reader.snapshot.Phone, "hy"); err != nil {
			t.Fatal(err)
		}
		sender.reject, sender.rejectWhatsApp = true, true
		want := []Channel{Telegram, WhatsApp, SMS}
		if uncertain {
			sender.uncertainChannel = WhatsApp
			want = []Channel{Telegram, WhatsApp}
		}
		id := ingestAndDeliver(t, s, "create")
		if err := s.Deliver(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sender.channels, want) {
			t.Fatalf("routes: %v; want %v", sender.channels, want)
		}
	}
}
