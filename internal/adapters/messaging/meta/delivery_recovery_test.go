package meta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestWhatsAppRecoveryDoesNotReopenAnExpiredWindow(t *testing.T) {
	for _, tc := range []struct {
		name, timestamp string
		want            int
	}{
		{"recent", strconv.FormatInt(receivedAt.Add(-time.Hour).Unix(), 10), 1},
		{"old", strconv.FormatInt(receivedAt.Add(-25*time.Hour).Unix(), 10), 0},
		{"boundary", strconv.FormatInt(receivedAt.Add(-24*time.Hour).Unix(), 10), 0},
		{"missing", "", 0},
		{"invalid", "invalid", 0},
		{"future", strconv.FormatInt(receivedAt.Add(10*time.Minute).Unix(), 10), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(string(fixture(t, "whatsapp_text.json")), "1788436800", tc.timestamp, 1))
			recorder := &recordingHandler{}
			feedback := &recoveryFeedback{}
			h := newHandler(recorder, t).WithFeedback(feedback)
			if response := post(t, h, sign(body), body); response.Code != http.StatusOK || len(recorder.got) != tc.want || feedback.calls != tc.want {
				t.Fatalf("status=%d handled=%d feedback=%d", response.Code, len(recorder.got), feedback.calls)
			}
		})
	}
}

type recoveryFeedback struct{ calls int }

func (f *recoveryFeedback) BeginFeedback(context.Context, messaging.Envelope) error {
	f.calls++
	return nil
}
func (*recoveryFeedback) EndFeedback(context.Context, messaging.Envelope) error { return nil }

func TestTerminalSendFailureAcknowledgesTheWebhook(t *testing.T) {
	for _, err := range []error{messaging.ErrDeliveryUncertain, messaging.ErrDeliveryRejected} {
		body := fixture(t, "whatsapp_text.json")
		h := newHandler(&recordingHandler{err: fmt.Errorf("send: %w", err)}, t)
		if response := post(t, h, sign(body), body); response.Code != http.StatusOK {
			t.Fatalf("terminal failure must not trigger redelivery: %d", response.Code)
		}
	}
}

func TestBusyDeliveryKeepsTheWebhookRetryable(t *testing.T) {
	body := fixture(t, "whatsapp_text.json")
	h := newHandler(&recordingHandler{err: fmt.Errorf("claim: %w", messaging.ErrDeliveryBusy)}, t)
	if response := post(t, h, sign(body), body); response.Code != http.StatusInternalServerError {
		t.Fatalf("unfinished delivery must not be acknowledged: %d", response.Code)
	}
}

type recoveryTransport func(*http.Request) (*http.Response, error)

func (f recoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLostSendResponseIsUncertain(t *testing.T) {
	client, _ := NewClient("token", "phone", WithHTTPClient(&http.Client{Transport: recoveryTransport(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}))
	err := client.Send(t.Context(), messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "recipient", Text: "answer"})
	if !errors.Is(err, messaging.ErrDeliveryUncertain) {
		t.Fatalf("lost POST response should not replay: %v", err)
	}
}

func TestPartialWhatsAppReplyIsNotAutomaticallyRepeated(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		t.Run(fmt.Sprint(interactive), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 2 {
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer srv.Close()
			client, _ := NewClient("token", "phone", WithBaseURL(srv.URL))
			msg := messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "recipient", Text: strings.Repeat("a", 4100)}
			if interactive {
				msg.Text = strings.Repeat("a", 1100)
				msg.Choices = []messaging.Choice{{Label: "Book"}}
			}
			if err := client.Send(t.Context(), msg); !errors.Is(err, messaging.ErrDeliveryUncertain) || calls != 2 {
				t.Fatalf("partial reply must not replay its accepted prefix: %d %v", calls, err)
			}
		})
	}
}

func TestDefiniteRateLimitCanRetryButRejectionCannot(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadRequest, http.StatusBadGateway} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		client, _ := NewClient("token", "phone", WithBaseURL(srv.URL))
		err := client.Send(t.Context(), messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "recipient", Text: "answer"})
		srv.Close()
		if terminal := messaging.TerminalDelivery(err); terminal != (status != http.StatusTooManyRequests) {
			t.Fatalf("status %d terminal=%v: %v", status, terminal, err)
		}
	}
}
