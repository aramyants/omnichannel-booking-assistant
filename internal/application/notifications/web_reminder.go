package notifications

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/google/uuid"
)

// QueueWebReminder accepts only a fresh signed worker handoff for the current
// verified booking. Its stable identity makes retries of a lost response safe.
func (s *Service) QueueWebReminder(ctx context.Context, recordID, phone, proof string, minutes int) error {
	if minutes != 60 && minutes != 120 && minutes != 1440 {
		return errors.New("invalid reminder choice")
	}
	record, err := s.Repo.GetNotification(ctx, Key("record", recordID))
	if err != nil {
		return err
	}
	if proof == "" || record.ManagementPhone != phone || record.CalendarPhone != phone || subtle.ConstantTimeCompare([]byte(record.ManagementToken), []byte(proof)) != 1 {
		return booking.ErrNotFound
	}
	snap, err := s.Reader.ReadNativeBooking(ctx, recordID)
	if err != nil {
		return err
	}
	due := snap.Booking.StartsAt.Add(-time.Duration(minutes) * time.Minute)
	if snap.Phone != phone || snap.Booking.Status != booking.StatusConfirmed || !snap.Booking.StartsAt.After(s.now()) || s.now().Before(due) || s.now().After(due.Add(15*time.Minute)) {
		return booking.ErrNotFound
	}
	fp := fingerprint(snap)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("web-reminder:"+recordID+":"+fp+":"+due.Format(time.RFC3339))).String()
	key := Key("event", id)
	err = s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		if rows[key].Kind == "" {
			*rows[key] = Entry{Kind: "event", State: "prepared", Event: Event{ID: id, RecordID: recordID, Status: "reminder", ReceivedAt: s.now()}, Notice: Notice{ID: id, Snapshot: snap, Purpose: BookingReminder, Fingerprint: fp}, UpdatedAt: s.now()}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.schedule(ctx, id, "initial")
}
