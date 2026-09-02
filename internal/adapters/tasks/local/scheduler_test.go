package local

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/reminders"
)

func TestSchedulerRunsADeterministicTaskOnce(t *testing.T) {
	called := make(chan string, 1)
	var calls atomic.Int32
	scheduler, err := New(func(_ context.Context, reminderID string) error {
		calls.Add(1)
		called <- reminderID
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer scheduler.Close()

	task := reminders.Task{ID: "task-1", ReminderID: "reminder-1", RunAt: time.Now().Add(20 * time.Millisecond)}
	if err := scheduler.Schedule(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Schedule(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-called:
		if got != "reminder-1" {
			t.Errorf("reminder id = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled task did not run")
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Errorf("duplicate task ran %d times, want 1", calls.Load())
	}
}

func TestCloseStopsPendingTasks(t *testing.T) {
	called := make(chan struct{}, 1)
	scheduler, err := New(func(context.Context, string) error {
		called <- struct{}{}
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Schedule(t.Context(), reminders.Task{
		ID: "later", ReminderID: "reminder", RunAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	scheduler.Close()

	select {
	case <-called:
		t.Error("a pending task ran after Close")
	case <-time.After(30 * time.Millisecond):
	}
}
