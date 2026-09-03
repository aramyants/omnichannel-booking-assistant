package meta

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// MessageHandler receives normalised messages parsed from a webhook delivery.
type MessageHandler interface {
	Handle(ctx context.Context, msg messaging.Envelope) error
}

// Handler serves the webhook endpoint for one Meta channel.
//
// It answers both halves of the contract: the GET that completes subscription,
// and the signed POSTs that carry messages.
type Handler struct {
	webhook  *Webhook
	messages MessageHandler
	logger   *slog.Logger
	parse    func(body []byte, receivedAt time.Time) ([]messaging.Envelope, error)
	now      func() time.Time
}

// NewWhatsAppHandler returns the handler for the WhatsApp webhook endpoint.
func NewWhatsAppHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger) *Handler {
	return &Handler{
		webhook:  webhook,
		messages: messages,
		logger:   logger,
		parse:    ParseWhatsApp,
		now:      time.Now,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.serveChallenge(w, r)
		return
	}
	h.serveDelivery(w, r)
}

// serveChallenge answers Meta's subscription handshake.
func (h *Handler) serveChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, ok := h.webhook.VerifyChallenge(r.URL.Query())
	if !ok {
		h.logger.WarnContext(r.Context(), "rejected a meta subscription attempt",
			"remote_addr", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	h.logger.InfoContext(r.Context(), "completed a meta webhook subscription")
	ChallengeResponse(w, challenge)
}

// serveDelivery verifies, parses and processes one signed delivery.
//
// The status returned decides whether Meta redelivers, so the two failure modes
// are answered differently, exactly as for any other provider: a body that
// cannot be parsed will never parse and is acknowledged, while a message that
// parsed but could not be handled is answered with an error so it is sent again.
func (h *Handler) serveDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Read first: the signature covers the exact bytes Meta sent, so nothing
	// may touch the body before it is checked.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.ErrorContext(ctx, "could not read a meta delivery", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !h.webhook.VerifySignature(r.Header.Get(SignatureHeader), body) {
		// Deliberately terse: an attacker probing the endpoint learns nothing,
		// and the detail goes to the log instead.
		h.logger.WarnContext(ctx, "rejected a meta delivery with an invalid signature",
			"remote_addr", r.RemoteAddr, "bytes", len(body))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	envelopes, err := h.parse(body, h.now().UTC())
	if err != nil {
		h.logger.ErrorContext(ctx, "discarded an unparseable meta delivery",
			"error", err, "bytes", len(body))
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, envelope := range envelopes {
		if err := h.messages.Handle(ctx, envelope); err != nil {
			if errors.Is(err, context.Canceled) {
				h.logger.WarnContext(ctx, "abandoned a meta message mid-flight",
					"dedupe_key", envelope.DedupeKey())
			} else {
				h.logger.ErrorContext(ctx, "could not process a meta message",
					"error", err, "dedupe_key", envelope.DedupeKey())
			}
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
