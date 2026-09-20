package reminders

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type fakeReader struct {
	b   booking.Booking
	err error
}

func (r fakeReader) ReadBooking(context.Context, booking.Booking) (booking.Booking, error) {
	return r.b, r.err
}

type templateSender struct {
	fakeSender
	templateCalls int
}

func (s *templateSender) SupportsReminder(lang string) bool { return lang == "en" }
func (s *templateSender) SendReminder(_ context.Context, _ messaging.Outgoing, _ string, _ booking.Booking, _ string) error {
	s.templateCalls++
	return nil
}

func TestReminderRechecksExternalCancellationAndMove(t *testing.T) {
	for _, scenario := range []string{"cancelled", "moved", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			now := testNow
			scheduler := &fakeScheduler{}
			sender := &fakeSender{}
			svc, store := newTestService(t, &now, scheduler, sender)
			start := now.Add(72 * time.Hour)
			task := plan(t, svc, store, start)
			now = start.Add(-24 * time.Hour)
			b := appointment(start)
			reader := fakeReader{b: b}
			switch scenario {
			case "cancelled":
				reader.b.Status = booking.StatusCancelled
			case "moved":
				reader.b.StartsAt = start.Add(48 * time.Hour)
			case "offline":
				reader.err = errors.New("offline")
			}
			svc.reader = reader
			err := svc.Deliver(t.Context(), task.ReminderID)
			if (err != nil) != (scenario == "offline") {
				t.Fatalf("delivery error %v", err)
			}
			if sender.count() != 0 {
				t.Fatal("sent stale reminder")
			}
			if scenario == "moved" && len(scheduler.snapshot()) != 2 {
				t.Fatal("moved visit was not rescheduled")
			}
		})
	}
}

func TestWhatsAppReminderRequiresConsentAndApprovedLanguage(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	svc, store := newTestService(t, &now, scheduler, &fakeSender{})
	sender := &templateSender{}
	svc.senders[messaging.ProviderWhatsApp] = sender
	svc.conversations = store
	conv := reminderConversation()
	conv.Provider = messaging.ProviderWhatsApp
	b := appointment(now.Add(72 * time.Hour))
	if err := store.SaveBooking(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := svc.Plan(t.Context(), b, conv, "en"); err != nil {
		t.Fatal(err)
	}
	if len(scheduler.snapshot()) != 0 {
		t.Fatal("scheduled without consent")
	}
	conv.ReminderOptIn = true
	if err := svc.Plan(t.Context(), b, conv, "ru"); err != nil {
		t.Fatal(err)
	}
	if len(scheduler.snapshot()) != 0 {
		t.Fatal("scheduled untranslated template")
	}
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if err := svc.Plan(t.Context(), b, conv, "en"); err != nil {
		t.Fatal(err)
	}
	task := scheduler.snapshot()[0]
	now = task.RunAt
	conv.ReminderOptIn = false
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
		t.Fatal(err)
	}
	if sender.templateCalls != 0 || sender.count() != 0 {
		t.Fatal("sent after opt-out")
	}
}
