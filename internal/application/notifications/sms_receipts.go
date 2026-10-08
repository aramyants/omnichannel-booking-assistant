package notifications

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SMSDeliveryRepository reads only accepted/uncertain SMS events. It includes
// historical events so existing queued messages can acquire their final status.
type SMSDeliveryRepository interface {
	PendingSMSNotifications(context.Context) ([]Entry, error)
}

func awaitingSMS(row Entry) bool {
	return row.Kind == "event" && row.State == "done" &&
		(row.Outcome == "accepted_sms" || row.Outcome == "uncertain_sms")
}

func (s *Service) reconcileSMS(ctx context.Context) error {
	if s.SMSStatus == nil {
		return nil
	}
	repo, ok := s.Repo.(SMSDeliveryRepository)
	if !ok {
		return errors.New("SMS delivery repository is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	rows, err := repo.PendingSMSNotifications(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, row := range rows {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		result = errors.Join(result, s.checkSMS(ctx, row.Event.ID))
	}
	return result
}

func (s *Service) checkSMS(ctx context.Context, id string) error {
	key, owner, now := Key("event", id), uuid.NewString(), s.now()
	claimed := false
	var notice Notice
	if err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		claimed = false
		row := rows[key]
		if !awaitingSMS(*row) || row.LeaseUntil.After(now) || row.NextLookupAt.After(now) {
			return nil
		}
		row.LeaseOwner, row.LeaseUntil = owner, now.Add(time.Minute)
		// Rotate even failed reads through the bounded query. One unavailable
		// provider message must not starve newer delivery checks indefinitely.
		row.UpdatedAt, row.NextLookupAt = now, now.Add(5*time.Minute)
		notice, claimed = row.Notice, true
		return nil
	}); err != nil || !claimed {
		return err
	}
	state, readErr := s.SMSStatus(ctx, notice)
	alert := false
	var recordID string
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err := s.Repo.TransactNotifications(cleanupCtx, []string{key}, func(rows map[string]*Entry) error {
		alert = false
		row := rows[key]
		if row.LeaseOwner != owner || !awaitingSMS(*row) {
			return nil
		}
		row.LeaseOwner, row.LeaseUntil = "", time.Time{}
		expired := !row.Event.ReceivedAt.IsZero() && !now.Before(row.Event.ReceivedAt.Add(24*time.Hour))
		if readErr != nil {
			// Missing status proves neither delivery nor non-delivery. Keep its
			// existing evidence and stop polling after a bounded observation window.
			if expired {
				row.Outcome = "unconfirmed_sms"
			}
			return nil
		}
		switch state {
		case "Pending", "Processed", "Sent", "Cancelling", "Delivered":
			row.ProviderState = "sms_" + strings.ToLower(state)
			if expired {
				row.Outcome = "unconfirmed_sms"
				if state == "Delivered" {
					row.Outcome = "delivered_sms"
				}
			}
			// Multipart Delivered may mean only one part; keep observing for a
			// later Failed state instead of promising complete delivery.
		case "Failed", "Cancelled":
			row.ProviderState = "sms_" + strings.ToLower(state)
			row.Outcome = "failed_sms"
			if state == "Cancelled" {
				row.Outcome = "cancelled_sms"
			}
			if row.SMSFailureAlertedAt.IsZero() && s.SMSFailureAlert != nil {
				row.SMSFailureAlertedAt = now
				recordID, alert = row.Event.RecordID, true
			}
		default:
			return errors.New("unrecognized SMS delivery state")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if alert {
		// Alert intent is durable first. Unknown acceptance never spams staff.
		if err := s.SMSFailureAlert(cleanupCtx, recordID, id); err != nil {
			return fmt.Errorf("SMS failure alert requires attention: %w", err)
		}
	}
	return readErr
}
