// Package local provides an in-process reminder scheduler for development.
// Timers disappear on restart, so production uses Cloud Tasks instead.
package local

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/reminders"
)

const (
	deliveryTimeout = time.Minute
	retryDelay      = 10 * time.Second
)

// Handler delivers one reminder by id.
type Handler func(context.Context, string) error

// Scheduler owns the development process's reminder timers.
type Scheduler struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer
	handler Handler
	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	wg      sync.WaitGroup
}

// New returns an empty in-process scheduler.
func New(handler Handler, logger *slog.Logger) (*Scheduler, error) {
	if handler == nil {
		return nil, errors.New("local task scheduler: handler is required")
	}
	if logger == nil {
		return nil, errors.New("local task scheduler: logger is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		timers:  make(map[string]*time.Timer),
		handler: handler,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

// Schedule starts one timer. A duplicate deterministic task id is already
// scheduled and therefore succeeds without creating another timer.
func (s *Scheduler) Schedule(_ context.Context, task reminders.Task) error {
	if task.ID == "" || task.ReminderID == "" || task.RunAt.IsZero() {
		return errors.New("local task scheduler: task is incomplete")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("local task scheduler: closed")
	}
	if _, exists := s.timers[task.ID]; exists {
		return nil
	}

	delay := time.Until(task.RunAt)
	if delay < 0 {
		delay = 0
	}
	s.timers[task.ID] = time.AfterFunc(delay, func() { s.run(task) })
	return nil
}

func (s *Scheduler) run(task reminders.Task) {
	s.mu.Lock()
	delete(s.timers, task.ID)
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.ctx, deliveryTimeout)
	defer cancel()
	if err := s.handler(ctx, task.ReminderID); err != nil {
		s.logger.ErrorContext(ctx, "local reminder delivery failed; scheduling a retry",
			"error", err, "reminder_id", task.ReminderID)
		task.RunAt = time.Now().Add(retryDelay)
		if scheduleErr := s.Schedule(context.WithoutCancel(ctx), task); scheduleErr != nil {
			s.logger.ErrorContext(ctx, "could not retry local reminder delivery",
				"error", scheduleErr, "reminder_id", task.ReminderID)
		}
	}
}

// Close stops pending timers and waits for a running handler to observe
// cancellation and finish.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	for _, timer := range s.timers {
		timer.Stop()
	}
	s.timers = nil
	s.mu.Unlock()
	s.wg.Wait()
}
