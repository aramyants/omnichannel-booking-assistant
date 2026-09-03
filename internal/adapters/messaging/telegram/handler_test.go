package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type recordingHandler struct {
	got []messaging.Envelope
	err error
}

func (h *recordingHandler) Handle(_ context.Context, msg messaging.Envelope) error {
	h.got = append(h.got, msg)
	return h.err
}

func newTestHandler(messages MessageHandler) *Handler {
	return NewHandler(testWebhook(), messages, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func post(t *testing.T, h http.Handler, secret string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/telegram", strings.NewReader(string(body)))
	if secret != "" {
		req.Header.Set(SecretHeader, secret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerAcceptsAValidDelivery(t *testing.T) {
	messages := &recordingHandler{}
	rec := post(t, newTestHandler(messages), "s3cret-token", fixture(t, "text_message.json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 1 {
		t.Fatalf("handled %d messages, want 1", len(messages.got))
	}
	if messages.got[0].Content.Text != "Hi, can I book a haircut on Friday afternoon?" {
		t.Errorf("unexpected text %q", messages.got[0].Content.Text)
	}
}

// TestHandlerRejectsDeliveriesWithoutTheSecret is the endpoint's only
// authentication: Telegram publishes no source IP range and signs nothing.
func TestHandlerRejectsDeliveriesWithoutTheSecret(t *testing.T) {
	tests := map[string]string{
		"no secret at all": "",
		"the wrong secret": "guessed-token",
		"a near miss":      "s3cret-toke",
	}

	for name, secret := range tests {
		t.Run(name, func(t *testing.T) {
			messages := &recordingHandler{}
			rec := post(t, newTestHandler(messages), secret, fixture(t, "text_message.json"))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if len(messages.got) != 0 {
				t.Error("an unauthenticated delivery reached the application")
			}
		})
	}
}

// TestHandlerAcknowledgesUnparseableDeliveries records a deliberate choice: a
// body that cannot be parsed will never parse, so answering with an error would
// make Telegram redeliver it indefinitely.
func TestHandlerAcknowledgesUnparseableDeliveries(t *testing.T) {
	messages := &recordingHandler{}
	rec := post(t, newTestHandler(messages), "s3cret-token", []byte("{not json"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d so Telegram stops redelivering", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Error("a malformed delivery reached the application")
	}
}

// TestHandlerAsksForRedeliveryWhenProcessingFails is the counterpart: a message
// that parsed but could not be handled may succeed on a second attempt.
func TestHandlerAsksForRedeliveryWhenProcessingFails(t *testing.T) {
	messages := &recordingHandler{err: errors.New("telegram send failed")}
	rec := post(t, newTestHandler(messages), "s3cret-token", fixture(t, "text_message.json"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d so Telegram redelivers", rec.Code, http.StatusInternalServerError)
	}
}

// TestStaffChatIsNotTreatedAsACustomer: adding the bot to a staff group means
// the group's own messages arrive at this webhook. Answering colleagues as
// though they were customers would make the group unusable.
func TestStaffChatIsNotTreatedAsACustomer(t *testing.T) {
	messages := &recordingHandler{}

	// The group fixture's own chat id, nominated here as the staff group.
	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithStaffChat("-1001234567890"))

	rec := post(t, handler, "s3cret-token", fixture(t, "group_message.json"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Errorf("a staff message was handled as a customer: %+v", messages.got)
	}
}

// TestOtherChatsStillReachTheAssistant guards the obvious way to get that guard
// wrong: silencing every group rather than the one nominated.
func TestOtherChatsStillReachTheAssistant(t *testing.T) {
	messages := &recordingHandler{}

	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithStaffChat("-100999999999"))

	rec := post(t, handler, "s3cret-token", fixture(t, "group_message.json"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 1 {
		t.Errorf("handled %d messages, want the customer's to get through", len(messages.got))
	}
}

func TestHandlerAcknowledgesUpdatesWithNothingToAnswer(t *testing.T) {
	messages := &recordingHandler{}
	rec := post(t, newTestHandler(messages), "s3cret-token", fixture(t, "callback_query.json"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Error("an update with no message reached the application")
	}
}
