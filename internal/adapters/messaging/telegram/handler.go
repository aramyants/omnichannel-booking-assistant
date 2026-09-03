package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// MessageHandler receives normalised messages parsed from a webhook delivery.
//
// The interface is declared here, next to the code that calls it, so the
// adapter depends on a shape it needs rather than on a concrete service.
type MessageHandler interface {
	Handle(ctx context.Context, msg messaging.Envelope) error
}

// Handler serves Telegram webhook deliveries.
type Handler struct {
	webhook     *Webhook
	messages    MessageHandler
	logger      *slog.Logger
	staffChatID string
}

// HandlerOption customises a Handler.
type HandlerOption func(*Handler)

// WithStaffChat names the chat the business is notified in.
//
// Adding the bot to a staff group means the group's own messages arrive at this
// same webhook. Without this, colleagues talking among themselves would each be
// treated as a customer and answered by the assistant.
func WithStaffChat(chatID string) HandlerOption {
	return func(h *Handler) { h.staffChatID = chatID }
}

// NewHandler returns an http.Handler for the Telegram webhook endpoint.
func NewHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, opts ...HandlerOption) *Handler {
	h := &Handler{webhook: webhook, messages: messages, logger: logger}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ServeHTTP verifies, parses and processes one webhook delivery.
//
// The status code chosen here decides whether Telegram redelivers, so the two
// failure modes are answered differently:
//
//   - A body that cannot be parsed will never parse, however many times it is
//     sent. It is logged and answered 200, because redelivering it forever
//     would be pure load.
//   - A failure to process a message that did parse may well be transient. It
//     is answered 500 so Telegram delivers it again.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.webhook.VerifySecret(r.Header.Get(SecretHeader)) {
		// Deliberately terse: an attacker probing the endpoint learns nothing
		// from the response, and the detail goes to the log instead.
		h.logger.WarnContext(ctx, "rejected a telegram delivery with an invalid secret",
			"remote_addr", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.ErrorContext(ctx, "could not read a telegram delivery", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	envelopes, err := h.webhook.Parse(body)
	if err != nil {
		// Logged at error because it means either a Telegram change or a bug
		// here, but answered 200 so it is not redelivered indefinitely.
		h.logger.ErrorContext(ctx, "discarded an unparseable telegram delivery",
			"error", err, "bytes", len(body))
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, envelope := range envelopes {
		// Colleagues talking in the staff group are not customers, and must not
		// be answered as though they were.
		if h.staffChatID != "" && envelope.ExternalThreadID == h.staffChatID {
			h.logger.DebugContext(ctx, "ignored a message in the staff chat",
				"chat_id", h.staffChatID)
			continue
		}

		if err := h.messages.Handle(ctx, envelope); err != nil {
			if errors.Is(err, context.Canceled) {
				// The caller hung up or the process is shutting down. Telegram
				// will redeliver, which is the correct outcome.
				h.logger.WarnContext(ctx, "abandoned a telegram message mid-flight",
					"dedupe_key", envelope.DedupeKey())
			} else {
				h.logger.ErrorContext(ctx, "could not process a telegram message",
					"error", err, "dedupe_key", envelope.DedupeKey())
			}
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
