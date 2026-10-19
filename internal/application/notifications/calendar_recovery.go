package notifications

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"time"
)

type calendarChanges interface {
	ListChangedNativeBookings(context.Context, time.Time, time.Time) ([]string, error)
}
type unroutedRepository interface {
	UnroutedNotifications(context.Context) ([]Entry, error)
}

// Polling shares the webhook outbox and semantic fingerprint. A failed scan
// never advances the cursor; a ten-minute overlap catches delayed indexing.
func (s *Service) reconcileCalendar(ctx context.Context) error {
	reader, ok := s.Reader.(calendarChanges)
	if !ok {
		return nil
	}
	const key = "calendar_reconciliation_v1"
	owner := uuid.NewString()
	now := s.now()
	var cursor Entry
	claimed := false
	err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		r := rows[key]
		if r.LeaseUntil.After(now) {
			return nil
		}
		cursor = *r
		r.Kind = "calendar_cursor"
		r.LeaseOwner = owner
		r.LeaseUntil = now.Add(3 * time.Minute)
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.Repo.TransactNotifications(release, []string{key}, func(rows map[string]*Entry) error {
			if rows[key].LeaseOwner == owner {
				rows[key].LeaseUntil = time.Time{}
			}
			return nil
		})
	}()
	from := cursor.ChangedAt.Add(-10 * time.Minute)
	if cursor.ChangedAt.IsZero() {
		from = now.Add(-24 * time.Hour)
	}
	if from.Before(s.ActivatedAt) {
		from = s.ActivatedAt
	}
	to := now.Add(-20 * time.Second)
	if to.Sub(from) > 24*time.Hour {
		to = from.Add(24 * time.Hour)
	}
	if !to.After(from) {
		return nil
	}
	ids, err := reader.ListChangedNativeBookings(ctx, from, to)
	if err != nil {
		return err
	}
	for _, id := range ids {
		snap, readErr := s.Reader.ReadNativeBooking(ctx, id)
		if readErr != nil {
			return readErr
		}
		// Do not trust a remote filter to decide freshness, ownership or messaging.
		changed := snap.ChangedAt
		if changed.IsZero() {
			changed = snap.Booking.CreatedAt
		}
		if changed.Before(from) || changed.After(to) {
			continue
		}
		prior, readErr := s.Repo.GetNotification(ctx, Key("record", id))
		if readErr != nil {
			return readErr
		}
		if prior.Fingerprint == fingerprint(snap) {
			continue
		}
		status := "update"
		if prior.Kind == "" {
			status = "create"
		}
		eventID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("emotion-calendar-v1:"+id+":"+fingerprint(snap)+":"+changed.Format(time.RFC3339Nano))).String()
		if err = s.Ingest(ctx, Event{ID: eventID, RecordID: id, Status: status}); err != nil {
			return err
		}
	}
	return s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		r := rows[key]
		if r.LeaseOwner != owner {
			return ErrBusy
		}
		r.ChangedAt = to
		r.UpdatedAt = now
		r.Outcome = "success"
		return nil
	})
}

// A notice for which no route existed has no provider send intent. Only this
// proven-unsent state may be reopened; accepted and ambiguous sends stay final.
func (s *Service) reconcileUnrouted(ctx context.Context) error {
	repo, ok := s.Repo.(unroutedRepository)
	if !ok {
		return nil
	}
	entries, err := repo.UnroutedNotifications(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, row := range entries {
		if row.Kind != "event" || row.State != "done" || row.Outcome != "no_linked_messenger" || len(row.Attempted) > 0 || row.Event.ReceivedAt.Before(s.now().Add(-24*time.Hour)) {
			continue
		}
		key := Key("event", row.Event.ID)
		ready := false
		err = s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
			r := rows[key]
			if r.State == "done" && r.Outcome == "no_linked_messenger" && len(r.Attempted) == 0 && !r.LeaseUntil.After(s.now()) {
				r.State = "prepared"
				r.Outcome = ""
				ready = true
			}
			return nil
		})
		if err == nil && ready {
			err = s.schedule(ctx, row.Event.ID, "unrouted:"+s.now().Truncate(5*time.Minute).Format(time.RFC3339))
		}
		result = errors.Join(result, err)
	}
	return result
}
