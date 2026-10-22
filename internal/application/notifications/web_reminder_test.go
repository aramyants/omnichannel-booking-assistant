package notifications

import (
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

func TestWebReminderUsesConfirmationChannelAndIsIdempotent(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	now := s.now()
	reader.snapshot.Booking.StartsAt = now.Add(2 * time.Hour)
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	ingestAndDeliver(t, s, "create")
	sender.channels = nil
	record := repo.rows[Key("record", "42")]
	record.ManagementToken = "proof"
	record.ManagementPhone = reader.snapshot.Phone
	record.ConfirmationChannel = WhatsApp
	repo.rows[Key("record", "42")] = record
	s.WhatsAppTemplates["booking_reminder:en"] = "approved_reminder"
	for range 2 {
		if err := s.QueueWebReminder(t.Context(), "42", reader.snapshot.Phone, "proof", 120); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for _, row := range repo.rows {
		if row.Kind == "event" && row.Notice.Purpose == BookingReminder {
			ids = append(ids, row.Event.ID)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("duplicate reminder jobs: %v", ids)
	}
	for range 2 {
		if err := s.Deliver(t.Context(), ids[0]); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.channels) != 1 || sender.channels[0] != WhatsApp {
		t.Fatalf("reminder did not keep confirmation channel: %v", sender.channels)
	}
}

func TestWebReminderRejectsEarlyInvalidOrUnownedHandoffs(t *testing.T) {
	s, repo, reader, _ := notificationFixture(t)
	now := s.now()
	reader.snapshot.Booking.StartsAt = now.Add(2 * time.Hour)
	repo.rows[Key("record", "42")] = Entry{Kind: "record", CalendarPhone: reader.snapshot.Phone, ManagementPhone: reader.snapshot.Phone, ManagementToken: "proof"}
	for _, tc := range []struct {
		phone, proof string
		minutes      int
	}{{reader.snapshot.Phone, "proof", 0}, {reader.snapshot.Phone, "proof", 61}, {reader.snapshot.Phone, "wrong", 120}, {"+37499123456", "proof", 120}, {reader.snapshot.Phone, "proof", 60}} {
		if err := s.QueueWebReminder(t.Context(), "42", tc.phone, tc.proof, tc.minutes); err == nil {
			t.Fatalf("invalid handoff accepted: %+v", tc)
		}
	}
	reader.snapshot.Booking.Status = booking.StatusCancelled
	if err := s.QueueWebReminder(t.Context(), "42", reader.snapshot.Phone, "proof", 120); err == nil {
		t.Fatal("cancellation received reminder")
	}
}

func TestReminderRechecksReschedulesAndNeverSendsAfterExpiry(t *testing.T) {
	for _, mode := range []string{"rescheduled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, reader, sender := notificationFixture(t)
			now := s.now()
			reader.snapshot.Booking.StartsAt = now.Add(2 * time.Hour)
			repo.rows[Key("record", "42")] = Entry{Kind: "record", CalendarPhone: reader.snapshot.Phone, ManagementPhone: reader.snapshot.Phone, ManagementToken: "proof"}
			if err := s.QueueWebReminder(t.Context(), "42", reader.snapshot.Phone, "proof", 120); err != nil {
				t.Fatal(err)
			}
			var id string
			for _, row := range repo.rows {
				if row.Notice.Purpose == BookingReminder {
					id = row.Event.ID
				}
			}
			if mode == "rescheduled" {
				reader.snapshot.Booking.StartsAt = reader.snapshot.Booking.StartsAt.Add(time.Hour)
			} else {
				s.Now = func() time.Time { return now.Add(16 * time.Minute) }
			}
			if err := s.Deliver(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if len(sender.channels) != 0 || repo.rows[Key("event", id)].State != "done" {
				t.Fatal("stale reminder sent")
			}
		})
	}
}
