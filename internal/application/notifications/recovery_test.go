package notifications

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/google/uuid"
)

type recoveryScheduler struct {
	events []string
	fail   map[string]bool
}

func (s *recoveryScheduler) ScheduleNotification(_ context.Context, _, eventID string, _ time.Time) error {
	s.events = append(s.events, eventID)
	if s.fail[eventID] {
		return errors.New("task queue unavailable")
	}
	return nil
}

func TestRecoverySchedulesBacklogDespiteWhatsAppProbeFailure(t *testing.T) {
	s, repo, _, _ := notificationFixture(t)
	scheduler := &recoveryScheduler{}
	s.Scheduler = scheduler
	id := uuid.NewString()
	repo.rows[Key("event", id)] = Entry{Kind: "event", State: "pending", Event: Event{ID: id, RecordID: "42"}}
	probeErr := errors.New("Graph unavailable")
	s.Health = &HealthGuard{Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
		if !reflect.DeepEqual(scheduler.events, []string{id}) {
			t.Fatal("provider probe ran before durable work was recovered")
		}
		return messaging.ChannelHealth{}, probeErr
	}}
	if err := s.Reconcile(t.Context()); !errors.Is(err, probeErr) {
		t.Fatalf("probe failure was lost: %v", err)
	}
}

func TestRecoveryContinuesAfterOneTaskCannotBeScheduled(t *testing.T) {
	s, repo, _, _ := notificationFixture(t)
	scheduler := &recoveryScheduler{fail: map[string]bool{}}
	s.Scheduler = scheduler
	for range 3 {
		id := uuid.NewString()
		repo.rows[Key("event", id)] = Entry{Kind: "event", State: "pending", Event: Event{ID: id, RecordID: "42"}}
		scheduler.fail[id] = true
	}
	if err := s.Reconcile(t.Context()); err == nil || len(scheduler.events) != 3 {
		t.Fatalf("recovery stopped at the first queue error: events=%v err=%v", scheduler.events, err)
	}
}

func TestConfirmedFailureRemainsRecoverableWhenTaskEnqueueFails(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	s.SMSReady, sender.reject = true, true
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	id := ingestAndDeliver(t, s, "create")
	scheduler := &recoveryScheduler{fail: map[string]bool{id: true}}
	s.Scheduler = scheduler
	if err := s.WhatsAppStatus(t.Context(), "native-booking:"+id, reader.snapshot.Phone, "failed"); err == nil {
		t.Fatal("queue failure was lost")
	}
	if row := repo.rows[Key("event", id)]; row.State != "prepared" {
		t.Fatalf("confirmed non-delivery disappeared from recovery: %+v", row)
	}
	scheduler.fail = nil
	scheduler.events = nil
	if err := s.Reconcile(t.Context()); err != nil || !reflect.DeepEqual(scheduler.events, []string{id}) {
		t.Fatalf("fallback was not recovered: events=%v err=%v", scheduler.events, err)
	}
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp, SMS}) {
		t.Fatalf("recovered fallback routes: %v", sender.channels)
	}
}

func TestFailureReceiptBeforeProviderReturnsSurvivesCompletion(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	s.SMSReady, sender.reject = true, true
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	scheduler := &recoveryScheduler{fail: map[string]bool{}}
	s.Scheduler = scheduler
	sender.beforeReturn = func(target Target, notice Notice) {
		if target.Channel != WhatsApp {
			return
		}
		scheduler.fail[notice.ID] = true
		if err := s.WhatsAppStatus(t.Context(), "native-booking:"+notice.ID, reader.snapshot.Phone, "failed"); err == nil {
			t.Fatal("queue failure was lost")
		}
	}
	id := ingestAndDeliver(t, s, "create")
	if row := repo.rows[Key("event", id)]; row.State != "prepared" {
		t.Fatalf("provider acceptance overwrote earlier failed receipt: %+v", row)
	}
	sender.beforeReturn = nil
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp, SMS}) {
		t.Fatal(sender.channels)
	}
}

func TestDeliveredReceiptCancelsUnsentFallback(t *testing.T) {
	s, _, reader, sender := notificationFixture(t)
	s.SMSReady, sender.reject = true, true
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	id := ingestAndDeliver(t, s, "create")
	for _, state := range []string{"failed", "delivered"} {
		if err := s.WhatsAppStatus(t.Context(), "native-booking:"+id, reader.snapshot.Phone, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp}) {
		t.Fatalf("fallback repeated a delivered notice: %v", sender.channels)
	}
}

func TestDelayedFallbackDoesNotSendAfterAppointmentStarts(t *testing.T) {
	s, repo, reader, sender := notificationFixture(t)
	s.SMSReady, sender.reject = true, true
	if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "en"); err != nil {
		t.Fatal(err)
	}
	id := ingestAndDeliver(t, s, "create")
	if err := s.WhatsAppStatus(t.Context(), "native-booking:"+id, reader.snapshot.Phone, "failed"); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return reader.snapshot.Booking.StartsAt }
	if err := s.Deliver(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.channels, []Channel{Telegram, WhatsApp}) || repo.rows[Key("event", id)].Outcome != "appointment_started" {
		t.Fatalf("late notice was sent: routes=%v outcome=%v", sender.channels, repo.rows[Key("event", id)].Outcome)
	}
}
