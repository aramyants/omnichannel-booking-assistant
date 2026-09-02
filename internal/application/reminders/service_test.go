package reminders

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
)

var testNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

type fakeScheduler struct {
	mu    sync.Mutex
	tasks []Task
	err   error
}

func (s *fakeScheduler) Schedule(_ context.Context, task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	return s.err
}

func (s *fakeScheduler) snapshot() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Task(nil), s.tasks...)
}

type fakeSender struct {
	mu      sync.Mutex
	sent    []messaging.Outgoing
	err     error
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *fakeSender) Send(_ context.Context, msg messaging.Outgoing) error {
	if s.entered != nil {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, msg)
	return nil
}

func (s *fakeSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func appointment(startsAt time.Time) booking.Booking {
	return booking.Booking{
		ID: "bk-1", ExternalID: "998877", CustomerID: "cust-1",
		StartsAt: startsAt, Duration: time.Hour, Status: booking.StatusConfirmed,
		CreatedAt: testNow,
	}
}

func reminderConversation() conversation.Conversation {
	return conversation.Conversation{
		ID: "conv-1", CustomerID: "cust-1", Provider: messaging.ProviderTelegram,
		ExternalThreadID: "thread-1", State: conversation.StateAssistantActive,
	}
}

func newTestService(
	t *testing.T,
	now *time.Time,
	scheduler *fakeScheduler,
	sender *fakeSender,
) (*Service, *memory.Store) {
	t.Helper()
	store := memory.New(memory.WithClock(func() time.Time { return *now }))
	svc, err := NewService(Deps{
		Repository: store,
		Bookings:   store,
		Messages:   store,
		Senders: map[messaging.Provider]Sender{
			messaging.ProviderTelegram: sender,
		},
		Scheduler: scheduler,
		LeadTime:  24 * time.Hour,
		Location:  time.UTC,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return *now },
	})
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	return svc, store
}

func plan(t *testing.T, svc *Service, store *memory.Store, startsAt time.Time) Task {
	t.Helper()
	b := appointment(startsAt)
	if err := store.SaveBooking(t.Context(), b); err != nil {
		t.Fatalf("SaveBooking() returned error: %v", err)
	}
	if err := svc.Plan(t.Context(), b, reminderConversation()); err != nil {
		t.Fatalf("Plan() returned error: %v", err)
	}
	tasks := svc.scheduler.(*fakeScheduler).snapshot()
	if len(tasks) == 0 {
		t.Fatal("Plan() scheduled no task")
	}
	return tasks[len(tasks)-1]
}

func TestPlanSchedulesAtTheLeadTimeAndIsDeterministic(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	svc, store := newTestService(t, &now, scheduler, &fakeSender{})
	b := appointment(now.Add(72 * time.Hour))

	for range 2 {
		if err := store.SaveBooking(t.Context(), b); err != nil {
			t.Fatal(err)
		}
		if err := svc.Plan(t.Context(), b, reminderConversation()); err != nil {
			t.Fatalf("Plan() returned error: %v", err)
		}
	}
	tasks := scheduler.snapshot()
	if len(tasks) != 2 {
		t.Fatalf("scheduled %d calls, want 2 idempotent attempts", len(tasks))
	}
	wantRunAt := b.StartsAt.Add(-24 * time.Hour)
	if !tasks[0].RunAt.Equal(wantRunAt) {
		t.Errorf("run at = %s, want %s", tasks[0].RunAt, wantRunAt)
	}
	if tasks[0].ID != tasks[1].ID || tasks[0].ReminderID != tasks[1].ReminderID {
		t.Error("the same booking version produced different task identities")
	}
}

func TestAppointmentInsideLeadWindowGetsNoImmediateReminder(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	svc, _ := newTestService(t, &now, scheduler, &fakeSender{})

	if err := svc.Plan(t.Context(), appointment(now.Add(12*time.Hour)), reminderConversation()); err != nil {
		t.Fatalf("Plan() returned error: %v", err)
	}
	if len(scheduler.snapshot()) != 0 {
		t.Error("a booking inside the reminder window scheduled an immediate message")
	}
}

func TestLongHorizonReminderChainsWithinCloudTasksLimit(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	svc, store := newTestService(t, &now, scheduler, &fakeSender{})
	task := plan(t, svc, store, now.Add(90*24*time.Hour))

	if want := testNow.Add(maxTaskDelay); !task.RunAt.Equal(want) {
		t.Errorf("first wake = %s, want horizon %s", task.RunAt, want)
	}
	now = task.RunAt
	if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
		t.Fatalf("early Deliver() returned error: %v", err)
	}
	tasks := scheduler.snapshot()
	if len(tasks) != 2 {
		t.Fatalf("early wake produced %d total tasks, want a chained second task", len(tasks))
	}
	if !tasks[1].RunAt.After(tasks[0].RunAt) || tasks[1].ID == tasks[0].ID {
		t.Errorf("chained task = %+v after %+v", tasks[1], tasks[0])
	}
}

func TestDeliverSendsOnceAndRecordsTheTranscript(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	sender := &fakeSender{}
	svc, store := newTestService(t, &now, scheduler, sender)
	task := plan(t, svc, store, now.Add(72*time.Hour))
	now = task.RunAt

	if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
		t.Fatalf("Deliver() returned error: %v", err)
	}
	if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
		t.Fatalf("duplicate Deliver() returned error: %v", err)
	}
	if sender.count() != 1 {
		t.Errorf("sent %d reminders, want exactly 1", sender.count())
	}
	stored, err := store.FindReminder(t.Context(), task.ReminderID)
	if err != nil || stored.Status != reminder.StatusSent {
		t.Errorf("stored reminder = %+v, %v; want sent", stored, err)
	}
	history, err := store.Recent(t.Context(), "conv-1", 10)
	if err != nil || len(history) != 1 || history[0].Direction != conversation.DirectionOutbound {
		t.Errorf("history = %+v, %v; want the delivered reminder", history, err)
	}
}

func TestConcurrentTaskDeliveriesSendOnlyOnce(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	sender := &fakeSender{entered: make(chan struct{}), release: make(chan struct{})}
	svc, store := newTestService(t, &now, scheduler, sender)
	task := plan(t, svc, store, now.Add(72*time.Hour))
	now = task.RunAt

	results := make(chan error, 2)
	go func() { results <- svc.Deliver(t.Context(), task.ReminderID) }()
	<-sender.entered
	go func() { results <- svc.Deliver(t.Context(), task.ReminderID) }()
	close(sender.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("Deliver() returned error: %v", err)
		}
	}
	if sender.count() != 1 {
		t.Errorf("concurrent tasks sent %d reminders, want 1", sender.count())
	}
}

func TestCancelledOrRescheduledAppointmentSkipsOldReminder(t *testing.T) {
	tests := map[string]func(*booking.Booking){
		"cancelled":   func(b *booking.Booking) { b.Status = booking.StatusCancelled },
		"rescheduled": func(b *booking.Booking) { b.StartsAt = b.StartsAt.Add(24 * time.Hour) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			now := testNow
			scheduler := &fakeScheduler{}
			sender := &fakeSender{}
			svc, store := newTestService(t, &now, scheduler, sender)
			task := plan(t, svc, store, now.Add(72*time.Hour))
			booked, _ := store.ListBookings(t.Context(), "cust-1")
			change(&booked[0])
			_ = store.SaveBooking(t.Context(), booked[0])
			now = task.RunAt

			if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
				t.Fatalf("Deliver() returned error: %v", err)
			}
			if sender.count() != 0 {
				t.Error("an obsolete reminder was sent")
			}
			stored, _ := store.FindReminder(t.Context(), task.ReminderID)
			if stored.Status != reminder.StatusSkipped {
				t.Errorf("status = %q, want skipped", stored.Status)
			}
		})
	}
}

func TestFailedSendReleasesTheReminderForRetry(t *testing.T) {
	now := testNow
	scheduler := &fakeScheduler{}
	sender := &fakeSender{err: errors.New("telegram unavailable")}
	svc, store := newTestService(t, &now, scheduler, sender)
	task := plan(t, svc, store, now.Add(72*time.Hour))
	now = task.RunAt

	if err := svc.Deliver(t.Context(), task.ReminderID); err == nil {
		t.Fatal("Deliver() succeeded despite the failed send")
	}
	stored, _ := store.FindReminder(t.Context(), task.ReminderID)
	if stored.Status != reminder.StatusScheduled {
		t.Errorf("status = %q, want it released for retry", stored.Status)
	}

	sender.err = nil
	if err := svc.Deliver(t.Context(), task.ReminderID); err != nil {
		t.Fatalf("retry Deliver() returned error: %v", err)
	}
	if sender.count() != 1 {
		t.Errorf("retry sent %d reminders, want 1", sender.count())
	}
}
