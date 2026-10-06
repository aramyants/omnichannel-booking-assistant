package notifications

import (
	"context"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/google/uuid"
)

// WhatsAppStatus is invoked only by the signed webhook for our business number.
// A failure proves non-delivery; missing receipts and timeouts do not.
func (s *Service) WhatsAppStatus(ctx context.Context, reference, rawPhone, state string) error {
	id, ok := strings.CutPrefix(reference, "native-booking:")
	if !ok {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil
	}
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return nil
	}
	if state != "sent" && state != "delivered" && state != "read" && state != "failed" {
		return nil
	}
	key := Key("event", id)
	event, err := s.Repo.GetNotification(ctx, key)
	if err != nil {
		return err
	}
	if event.Kind != "event" || event.Notice.ID != id {
		return nil
	}
	boundPhone, err := customer.NormalizePhone(event.Notice.Snapshot.Phone)
	if err != nil || boundPhone != phone {
		return nil
	}
	deliveryKey := Key("delivery", event.Event.RecordID+":"+id)
	failed := false
	err = s.Repo.TransactNotifications(ctx, []string{deliveryKey}, func(rows map[string]*Entry) error {
		row := rows[deliveryKey]
		attempted := false
		for _, channel := range row.Attempted {
			if channel == string(WhatsApp) {
				attempted = true
			}
		}
		if !attempted {
			return nil
		}
		// Delivered/read receipts are terminal evidence even if Meta retries an
		// older failure later. A sent receipt cannot overwrite a failure.
		if row.ProviderState == "read" || (row.ProviderState == "delivered" && state != "read") {
			return nil
		}
		if row.ProviderState == "failed" && state == "sent" {
			return nil
		}
		row.ProviderState = state
		row.UpdatedAt = s.now()
		failed = state == "failed"
		return nil
	})
	if err != nil {
		return err
	}
	if failed {
		return s.schedule(ctx, id, "whatsapp-failed")
	}
	return nil
}

func (s *Service) resumeFailedWhatsApp(ctx context.Context, id string) error {
	key := Key("event", id)
	event, err := s.Repo.GetNotification(ctx, key)
	if err != nil || event.Kind != "event" {
		return err
	}
	deliveryKey := Key("delivery", event.Event.RecordID+":"+id)
	return s.Repo.TransactNotifications(ctx, []string{key, deliveryKey}, func(rows map[string]*Entry) error {
		event, delivery := rows[key], rows[deliveryKey]
		if delivery.ProviderState != "failed" ||
			(delivery.Outcome != "accepted_whatsapp" && delivery.Outcome != "uncertain_whatsapp") {
			return nil
		}
		if event.LeaseUntil.After(s.now()) {
			return ErrBusy
		}
		if event.State != "done" && event.State != "prepared" {
			return nil
		}
		if event.State == "done" && event.Outcome != "accepted_whatsapp" && event.Outcome != "uncertain_whatsapp" {
			return nil
		}
		event.State = "prepared"
		event.Outcome = ""
		delivery.State = "retryable_rejection"
		delivery.Outcome = "rejected_whatsapp"
		return nil
	})
}
