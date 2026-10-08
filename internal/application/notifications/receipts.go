package notifications

import (
	"context"
	"strings"
	"time"

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
	err = s.Repo.TransactNotifications(ctx, []string{key, deliveryKey}, func(rows map[string]*Entry) error {
		failed = false
		event, row := rows[key], rows[deliveryKey]
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
		if failed {
			// Persist recoverable work before enqueueing. A Cloud Tasks outage
			// must leave fallback visible to the periodic recovery worker.
			prepareWhatsAppFallback(event, row, s.now())
		} else if (state == "delivered" || state == "read") && row.State == "retryable_rejection" && row.Outcome == "rejected_whatsapp" {
			// A later delivery receipt supersedes the earlier failure until a
			// fallback send intent has been committed.
			row.State, row.Outcome = "done", "accepted_whatsapp"
			if event.State == "prepared" {
				event.State, event.Outcome, event.UpdatedAt = "done", "accepted_whatsapp", s.now()
			}
		}
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
		if event.LeaseUntil.After(s.now()) {
			if delivery.ProviderState == "failed" && (delivery.Outcome == "accepted_whatsapp" || delivery.Outcome == "uncertain_whatsapp") {
				return ErrBusy
			}
			return nil
		}
		prepareWhatsAppFallback(event, delivery, s.now())
		return nil
	})
}

func prepareWhatsAppFallback(event, delivery *Entry, now time.Time) {
	if delivery.ProviderState != "failed" ||
		(delivery.Outcome != "accepted_whatsapp" && delivery.Outcome != "uncertain_whatsapp") {
		return
	}
	if event.State != "done" && event.State != "prepared" {
		return
	}
	if event.State == "done" && event.Outcome != "accepted_whatsapp" && event.Outcome != "uncertain_whatsapp" {
		return
	}
	event.State, event.Outcome, event.UpdatedAt = "prepared", "", now
	delivery.State, delivery.Outcome, delivery.UpdatedAt = "retryable_rejection", "rejected_whatsapp", now
}
