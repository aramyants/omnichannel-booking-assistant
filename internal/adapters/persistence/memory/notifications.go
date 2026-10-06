package memory

import (
	"context"
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
)

func cloneNotification(entry notifications.Entry) (notifications.Entry, error) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return notifications.Entry{}, err
	}
	var clone notifications.Entry
	err = json.Unmarshal(raw, &clone)
	return clone, err
}
func (s *Store) GetNotification(_ context.Context, key string) (notifications.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneNotification(s.notificationEntries[key])
}
func (s *Store) TransactNotifications(_ context.Context, keys []string, fn func(map[string]*notifications.Entry) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := map[string]*notifications.Entry{}
	for _, key := range keys {
		row, err := cloneNotification(s.notificationEntries[key])
		if err != nil {
			return err
		}
		rows[key] = &row
	}
	if err := fn(rows); err != nil {
		return err
	}
	for _, key := range keys {
		row, err := cloneNotification(*rows[key])
		if err != nil {
			return err
		}
		s.notificationEntries[key] = row
	}
	return nil
}
func (s *Store) PendingNotifications(_ context.Context) ([]notifications.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []notifications.Entry{}
	for _, entry := range s.notificationEntries {
		if entry.Kind != "event" || entry.State == "done" {
			continue
		}
		row, err := cloneNotification(entry)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
