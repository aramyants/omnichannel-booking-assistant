package main

import (
	"context"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegram"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramStaffAlertRetriesOnlyDefiniteRejections(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		rejected bool
	}{
		{"accepted", nil, false},
		{"rate limited", &telegram.APIError{StatusCode: 429, Code: 429}, true},
		{"blocked", &telegram.APIError{StatusCode: 403, Code: 403}, true},
		{"unreadable error", &telegram.APIError{StatusCode: 400}, false},
		{"server error", &telegram.APIError{StatusCode: 503, Code: 503}, false},
		{"transport unknown", context.DeadlineExceeded, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := telegramAlertRejected(test.err); got != test.rejected {
				t.Fatalf("definite rejection=%v, want %v", got, test.rejected)
			}
		})
	}
}

type ingestSpy struct {
	calls int
	event notifications.Event
}

type taskAuth struct{ err error }

func (a taskAuth) Authorize(context.Context, string) error { return a.err }

type deliverySpy struct{ delivered, reconciled int }

func (s *deliverySpy) Deliver(context.Context, string) error { s.delivered++; return nil }
func (s *deliverySpy) Reconcile(context.Context) error       { s.reconciled++; return nil }
func TestNativeTaskRejectsUnauthenticatedWork(t *testing.T) {
	for _, test := range []struct {
		auth          error
		body          string
		status, calls int
	}{
		{errors.New("bad identity"), `{"reconcile":true}`, 401, 0},
		{nil, `{"event_id":"bad"}`, 400, 0},
		{nil, `{"reconcile":true}`, 204, 1},
	} {
		spy := new(deliverySpy)
		h := notificationTaskHandler(taskAuth{test.auth}, spy, slog.New(slog.NewTextHandler(io.Discard, nil)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "/tasks/notifications", strings.NewReader(test.body)))
		if w.Code != test.status || spy.reconciled+spy.delivered != test.calls {
			t.Fatal("task authorization or validation failed")
		}
	}
}

func (s *ingestSpy) Ingest(_ context.Context, e notifications.Event) error {
	s.calls++
	s.event = e
	return nil
}
func TestAltegioWebhookAuthenticatesBeforeQueuing(t *testing.T) {
	secret := strings.Repeat("a", 48)
	for _, test := range []struct {
		name, key, company, hook, status string
		want, calls                      int
	}{{"valid", secret, "1389810", "11111111-1111-4111-8111-111111111111", "create", 204, 1}, {"no capability", "", "1389810", "11111111-1111-4111-8111-111111111111", "create", 401, 0}, {"wrong location", secret, "3", "11111111-1111-4111-8111-111111111111", "create", 403, 0}, {"missing identity", secret, "1389810", "", "create", 400, 0}, {"unsupported status", secret, "1389810", "11111111-1111-4111-8111-111111111111", "bogus", 400, 0}} {
		t.Run(test.name, func(t *testing.T) {
			spy := new(ingestSpy)
			handler := notificationWebhookHandler(secret, "1389810", spy)
			r := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/altegio?key="+test.key, strings.NewReader(`{"company_id":`+test.company+`,"resource":"record","resource_id":42,"status":"`+test.status+`","data":{"client":{"phone":"UNTRUSTED"}}}`))
			r.Header.Set("X-Hook-Id", test.hook)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want || spy.calls != test.calls {
				t.Fatalf("status %d calls %d", w.Code, spy.calls)
			}
			if spy.calls > 0 && spy.event.RecordID != "42" {
				t.Fatal("record hint not preserved")
			}
		})
	}
}

func TestWhatsAppNotificationCommandLanguageIsExplicit(t *testing.T) {
	for _, test := range []struct {
		text, source, language string
		enabled, handled       bool
	}{
		{"/notifications", "", "en", true, true},
		{"/notifications ru", "hy", "ru", true, true},
		{"/notifications en", "ru", "en", true, true},
		{"/notifications_off", "hy", "hy", false, true},
		{"/notifications please book me", "en", "en", false, false},
		{"/notifications hy", "en", "en", false, false},
	} {
		enabled, lang, handled := whatsappNotificationPreference(test.text, test.source)
		if enabled != test.enabled || lang != test.language || handled != test.handled {
			t.Errorf("command %q incorrectly changed preference", test.text)
		}
	}
}
