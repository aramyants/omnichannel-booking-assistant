package notifications

import (
	"context"
	"errors"
	"time"
)

type unroutedRepository interface {
	UnroutedNotifications(context.Context) ([]Entry, error)
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
