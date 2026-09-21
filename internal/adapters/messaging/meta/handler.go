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

type externalReplyHandler interface {
	RecordExternalReply(context.Context, messaging.ExternalReply) error
}

type feedbackSender interface {
	BeginFeedback(context.Context, messaging.Envelope) error
	EndFeedback(context.Context, messaging.Envelope) error
}

type profileResolver interface {
	ResolveProfile(context.Context, messaging.Envelope) (messaging.Sender, error)
}

// Handler serves the webhook endpoint for one Meta channel.
//
// It answers both halves of the contract: the GET that completes subscription,
// and the signed POSTs that carry messages.
type Handler struct {
	webhook              *Webhook
	messages             MessageHandler
	logger               *slog.Logger
	parse                func(body []byte, receivedAt time.Time) ([]messaging.Envelope, error)
	parseExternalReplies func([]byte, time.Time) ([]messaging.ExternalReply, error)
	now                  func() time.Time
	feedback             feedbackSender
	profiles             profileResolver
}

// WithProfiles fills the provider-visible display name and handle before the
// customer record is opened, avoiding questions for information already
// available from Messenger or Instagram.
func (h *Handler) WithProfiles(profiles profileResolver) *Handler {
	h.profiles = profiles
	return h
}

// WithFeedback enables native read/typing feedback for this webhook.
func (h *Handler) WithFeedback(feedback feedbackSender) *Handler {
	h.feedback = feedback
	return h
}

// NewWhatsAppHandler returns the handler for the WhatsApp webhook endpoint.
func NewWhatsAppHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, phoneNumberIDs ...string) *Handler {
	phoneNumberID := ""
	if len(phoneNumberIDs) > 0 {
		phoneNumberID = phoneNumberIDs[0]
	}
	return &Handler{
		webhook:  webhook,
		messages: messages,
		logger:   logger,
		parse: func(body []byte, receivedAt time.Time) ([]messaging.Envelope, error) {
			return parseWhatsAppForNumber(body, receivedAt, phoneNumberID)
		},
		parseExternalReplies: func(body []byte, receivedAt time.Time) ([]messaging.ExternalReply, error) {
			return parseWhatsAppExternalReplies(body, receivedAt, phoneNumberID)
		},
		now: time.Now,
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

	receivedAt := h.now().UTC()
	// Apply staff activity before customer messages even when Meta batches them
	// in the opposite order. Both paths share the verified signature above.
	if h.parseExternalReplies != nil {
		replies, err := h.parseExternalReplies(body, receivedAt)
		if err != nil {
			h.logger.ErrorContext(ctx, "discarded malformed business app echoes", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		for _, reply := range replies {
			recorder, ok := h.messages.(externalReplyHandler)
			if !ok {
				h.logger.ErrorContext(ctx, "business app echo recorder is not configured")
				http.Error(w, "processing failed", http.StatusInternalServerError)
				return
			}
			if err := recorder.RecordExternalReply(ctx, reply); err != nil {
				h.logger.ErrorContext(ctx, "could not record a business app echo", "error", err)
				http.Error(w, "processing failed", http.StatusInternalServerError)
				return
			}
		}
	}
	envelopes, err := h.parse(body, receivedAt)
	if err != nil {
		h.logger.ErrorContext(ctx, "discarded an unparseable meta delivery",
			"error", err, "bytes", len(body))
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, envelope := range envelopes {
		if h.profiles != nil {
			profileCtx, cancel := context.WithTimeout(ctx, feedbackTimeout)
			profile, profileErr := h.profiles.ResolveProfile(profileCtx, envelope)
			cancel()
			if profileErr != nil {
				h.logger.DebugContext(ctx, "could not read a meta customer profile", "error", profileErr, "provider", envelope.Provider)
			} else {
				envelope.Sender = profile
			}
		}
		h.beginFeedback(ctx, envelope)
		err := h.messages.Handle(ctx, envelope)
		h.endFeedback(ctx, envelope)
		if err != nil {
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

const feedbackTimeout = 4 * time.Second

func (h *Handler) beginFeedback(ctx context.Context, envelope messaging.Envelope) {
	if h.feedback == nil {
		return
	}
	feedbackCtx, cancel := context.WithTimeout(ctx, feedbackTimeout)
	defer cancel()
	if err := h.feedback.BeginFeedback(feedbackCtx, envelope); err != nil {
		h.logger.DebugContext(ctx, "could not start meta typing feedback", "error", err, "provider", envelope.Provider)
	}
}

func (h *Handler) endFeedback(ctx context.Context, envelope messaging.Envelope) {
	if h.feedback == nil {
		return
	}
	feedbackCtx, cancel := context.WithTimeout(ctx, feedbackTimeout)
	defer cancel()
	if err := h.feedback.EndFeedback(feedbackCtx, envelope); err != nil {
		h.logger.DebugContext(ctx, "could not stop meta typing feedback", "error", err, "provider", envelope.Provider)
	}
}
