package main

import (
	"context"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/smsgate"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/config"
	"time"
)

type cabinetSMS struct{ client *smsgate.Client }

func (s cabinetSMS) SendCode(ctx context.Context, phone, text, id string, expiry time.Time) error {
	status, err := s.client.Send(ctx, smsgate.Message{ID: id, Phone: phone, Text: text, ValidUntil: expiry, Priority: 100})
	if err != nil {
		return err
	}
	if status.State == smsgate.Failed || status.State == smsgate.Cancelled {
		return errors.New("SMS verification delivery failed")
	}
	return nil
}
func openCabinet(cfg config.Config, store appStore) (*cabinet.Identity, error) {
	repo, ok := store.(notifications.Repository)
	if !ok || !cfg.Notifications.Enabled {
		return nil, nil
	}
	identity := &cabinet.Identity{Repo: repo, Secret: []byte(cfg.Notifications.WebhookSecret)}
	if cfg.Notifications.SMS.Enabled {
		credentials := cfg.Notifications.SMS
		client, err := smsgate.NewClient(smsgate.Config{APIBaseURL: credentials.APIBaseURL, Username: credentials.Username, Password: credentials.Password, DeviceID: credentials.DeviceID, SIMNumber: credentials.SIMNumber}, nil)
		if err != nil {
			return nil, err
		}
		identity.Sender = cabinetSMS{client: client}
	}
	return identity, nil
}
