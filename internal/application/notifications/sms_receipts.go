package notifications

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SMSDeliveryRepository reads accepted/uncertain SMS events and definitely
// rejected staff alerts. Historical events can acquire their final status.
type SMSDeliveryRepository interface {
	PendingSMSNotifications(context.Context) ([]Entry, error)
}

func awaitingSMS(row Entry) bool {
	return row.Kind == "event" && row.State == "done" &&
		(row.Outcome == "accepted_sms" || row.Outcome == "uncertain_sms" || row.Outcome == "sms_alert_retryable")
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
	alertOnly := false
	var recordID string
	var notice Notice
	if err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		claimed = false
		alertOnly = false
		row := rows[key]
		if !awaitingSMS(*row) || row.LeaseUntil.After(now) || row.NextLookupAt.After(now) {
			return nil
		}
		row.LeaseOwner, row.LeaseUntil = owner, now.Add(time.Minute)
		// Rotate even failed reads through the bounded query. One unavailable
		// provider message must not starve newer delivery checks indefinitely.
		row.UpdatedAt, row.NextLookupAt = now, now.Add(5*time.Minute)
		if row.Outcome == "sms_alert_retryable" {
			if s.SMSFailureAlert == nil {
				row.LeaseOwner, row.LeaseUntil = "", time.Time{}
				return nil
			}
			// The provider failure is already known. Reserve the next alert
			// before calling Telegram; do not depend on a later SMS status read.
			row.Outcome = smsFailureOutcome(row.ProviderState)
			row.SMSFailureAlertedAt = now
			row.LeaseOwner, row.LeaseUntil = "", time.Time{}
			recordID, claimed, alertOnly = row.Event.RecordID, true, true
			return nil
		}
		notice, claimed = row.Notice, true
		return nil
	}); err != nil || !claimed {
		return err
	}
	if alertOnly {
		return s.sendSMSFailureAlert(ctx, key, recordID, id, now)
	}
	state, readErr := s.SMSStatus(ctx, notice)
	alert := false
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
		if err := s.sendSMSFailureAlert(ctx, key, recordID, id, now); err != nil {
			return err
		}
	}
	return readErr
}

func smsFailureOutcome(providerState string) string {
	if providerState == "sms_cancelled" {
		return "cancelled_sms"
	}
	return "failed_sms"
}

func (s *Service) sendSMSFailureAlert(ctx context.Context, key, recordID, id string, reservedAt time.Time) error {
	// Give the alert its own budget after the Firestore reservation. Sharing
	// the transaction's remaining deadline can manufacture an ambiguous send.
	alertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	rejected, alertErr := s.SMSFailureAlert(alertCtx, recordID, id)
	cancel()
	if alertErr == nil {
		return nil
	}
	if rejected {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cleanupCancel()
		err := s.Repo.TransactNotifications(cleanupCtx, []string{key}, func(rows map[string]*Entry) error {
			row := rows[key]
			if !row.SMSFailureAlertedAt.Equal(reservedAt) || (row.Outcome != "failed_sms" && row.Outcome != "cancelled_sms") {
				return nil
			}
			// Only a definite non-acceptance releases this reservation. Keep
			// the final SMS failure evidence while pacing another staff alert.
			row.Outcome = "sms_alert_retryable"
			row.SMSFailureAlertedAt = time.Time{}
			return nil
		})
		if err != nil {
			return errors.Join(fmt.Errorf("SMS failure alert requires attention: %w", alertErr), err)
		}
	}
	// Unknown acceptance retains the reservation so retries cannot spam staff.
	return fmt.Errorf("SMS failure alert requires attention: %w", alertErr)
}
