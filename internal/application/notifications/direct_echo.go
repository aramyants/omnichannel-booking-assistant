package notifications

import (
	"context"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (s *Service) RecordDirectSend(ctx context.Context, provider messaging.Provider, id string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	key := Key("direct_outbound", string(provider)+":"+id)
	return s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		*rows[key] = Entry{Kind: "direct_outbound", State: "done", UpdatedAt: s.now(), Contact: Contact{ExpiresAt: s.now().Add(7 * 24 * time.Hour)}}
		return nil
	})
}
func (s *Service) IsDirectSend(ctx context.Context, provider messaging.Provider, id string) (bool, error) {
	row, err := s.Repo.GetNotification(ctx, Key("direct_outbound", string(provider)+":"+id))
	return row.Kind == "direct_outbound", err
}
