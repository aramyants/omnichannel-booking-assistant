package taskhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/reminder"
)

type fakeAuthorizer struct{ err error }

func (a fakeAuthorizer) Authorize(context.Context, string) error { return a.err }

type fakeDeliverer struct {
	id  string
	err error
}

func (d *fakeDeliverer) Deliver(_ context.Context, reminderID string) error {
	d.id = reminderID
	return d.err
}

func testHandler(t *testing.T, authErr, deliveryErr error) (*fakeDeliverer, http.Handler) {
	t.Helper()
	deliverer := &fakeDeliverer{err: deliveryErr}
	handler, err := New(
		fakeAuthorizer{err: authErr}, deliverer,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return deliverer, handler
}

func TestHandlerAuthenticatesThenDelivers(t *testing.T) {
	deliverer, handler := testHandler(t, nil, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/tasks/reminders",
		strings.NewReader(`{"reminder_id":"reminder-1"}`))
	req.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || deliverer.id != "reminder-1" {
		t.Errorf("status = %d, delivered = %q", response.Code, deliverer.id)
	}
}

func TestHandlerRefusesUnauthenticatedRequests(t *testing.T) {
	deliverer, handler := testHandler(t, errors.New("bad token"), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/", strings.NewReader(`{}`),
	))
	if response.Code != http.StatusUnauthorized || deliverer.id != "" {
		t.Errorf("status = %d, delivered = %q", response.Code, deliverer.id)
	}
}

func TestHandlerRetriesTransientDeliveryFailures(t *testing.T) {
	_, handler := testHandler(t, nil, errors.New("telegram unavailable"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
		strings.NewReader(`{"reminder_id":"reminder-1"}`)))
	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 so Cloud Tasks retries", response.Code)
	}
}

func TestHandlerAcknowledgesPermanentBadTasks(t *testing.T) {
	tests := map[string]struct {
		body        string
		deliveryErr error
	}{
		"malformed": {body: `not-json`},
		"missing reminder": {
			body:        `{"reminder_id":"gone"}`,
			deliveryErr: reminder.ErrNotFound,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, handler := testHandler(t, nil, tt.deliveryErr)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/", strings.NewReader(tt.body),
			))
			if response.Code != http.StatusNoContent {
				t.Errorf("status = %d, want permanent task acknowledged", response.Code)
			}
		})
	}
}
