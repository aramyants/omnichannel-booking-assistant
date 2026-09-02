// Package taskhttp exposes authenticated HTTP task handlers.
package taskhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
)

const maxTaskBody = 4 << 10

// Authorizer verifies the identity attached to a task request.
type Authorizer interface {
	Authorize(ctx context.Context, authorization string) error
}

// Deliverer processes one reminder id.
type Deliverer interface {
	Deliver(ctx context.Context, reminderID string) error
}

// New returns the reminder task endpoint.
func New(authorizer Authorizer, deliverer Deliverer, logger *slog.Logger) (http.Handler, error) {
	if authorizer == nil {
		return nil, errors.New("task handler: authorizer is required")
	}
	if deliverer == nil {
		return nil, errors.New("task handler: deliverer is required")
	}
	if logger == nil {
		return nil, errors.New("task handler: logger is required")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := authorizer.Authorize(r.Context(), r.Header.Get("Authorization")); err != nil {
			logger.WarnContext(r.Context(), "refused an unauthenticated task request", "error", err)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		var payload struct {
			ReminderID string `json:"reminder_id"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, maxTaskBody))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil || payload.ReminderID == "" {
			// An authenticated malformed task came from this deployment and will
			// never become valid on retry. Acknowledge it after logging.
			logger.ErrorContext(r.Context(), "discarded a malformed reminder task", "error", err)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := deliverer.Deliver(r.Context(), payload.ReminderID); err != nil {
			if errors.Is(err, reminder.ErrNotFound) {
				logger.ErrorContext(r.Context(), "discarded a task for a missing reminder",
					"reminder_id", payload.ReminderID)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			logger.ErrorContext(r.Context(), "reminder task failed and will be retried",
				"error", err, "reminder_id", payload.ReminderID)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), nil
}
