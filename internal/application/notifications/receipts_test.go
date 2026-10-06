package notifications

import (
	"context"
	"reflect"
	"testing"
)

func TestConfirmedWhatsAppFailureResumesAtSMSWithoutRepeatingMessengers(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	s.SMSReady = true
	if err := s.AllowPhone(context.Background(), reader.snapshot.Phone, "hy"); err != nil {
		t.Fatal(err)
	}
	sender.reject = true
	id := ingestAndDeliver(t, s, "create")
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp}) {
		t.Fatal(sender.channels)
	}
	if err := s.WhatsAppStatus(context.Background(), "native-booking:"+id, reader.snapshot.Phone, "failed"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Deliver(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp, SMS}) {
		t.Fatalf("duplicate or missing fallback: %v", sender.channels)
	}
	if row := repo.rows[Key("event", id)]; row.Outcome != "accepted_sms" {
		t.Fatal(row.Outcome)
	}
}

func TestWhatsAppReceiptsCannotCrossPhonesOrOverrideDelivered(t *testing.T) {
	for _, name := range []string{"wrong phone", "delivered", "read", "no receipt", "stale booking", "withdrawn"} {
		t.Run(name, func(t *testing.T) {
			s, _, reader, sender := notificationFixture(t)
			s.SMSReady = true
			if err := s.AllowPhone(context.Background(), reader.snapshot.Phone, "hy"); err != nil {
				t.Fatal(err)
			}
			sender.reject = true
			id := ingestAndDeliver(t, s, "create")
			phone := reader.snapshot.Phone
			if name == "wrong phone" {
				phone = "+37491234567"
			}
			if name == "delivered" || name == "read" {
				if err := s.WhatsAppStatus(context.Background(), "native-booking:"+id, phone, name); err != nil {
					t.Fatal(err)
				}
			}
			if name == "stale booking" {
				reader.snapshot.Booking.StartsAt = reader.snapshot.Booking.StartsAt.AddDate(0, 0, 1)
			}
			if name == "withdrawn" {
				if err := s.BlockPhone(context.Background(), phone); err != nil {
					t.Fatal(err)
				}
			}
			if name != "no receipt" {
				if err := s.WhatsAppStatus(context.Background(), "native-booking:"+id, phone, "failed"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Deliver(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp}) {
				t.Fatalf("unsafe fallback: %v", sender.channels)
			}
		})
	}
}
