package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const testToken = "123456:AAHtestTokenValue"

func testMessage() messaging.Outgoing {
	return messaging.Outgoing{
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: "219847362",
		Text:             "Thanks Anna, I have your message.",
	}
}

func TestSendPostsToTheBotMethod(t *testing.T) {
	var (
		gotPath string
		gotBody sendMessageRequest
		gotType string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99}}`))
	}))
	defer srv.Close()

	client := NewClient(testToken, WithBaseURL(srv.URL))
	if err := client.Send(t.Context(), testMessage()); err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	if want := "/bot" + testToken + "/sendMessage"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if gotBody.ChatID != "219847362" {
		t.Errorf("chat_id = %q, want 219847362", gotBody.ChatID)
	}
	if gotBody.Text != testMessage().Text {
		t.Errorf("text = %q, want %q", gotBody.Text, testMessage().Text)
	}
}

// TestSendOmitsParseMode guards a deliberate choice: with a parse mode set,
// Telegram would interpret markup in a customer's own words.
func TestSendOmitsParseMode(t *testing.T) {
	var raw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := NewClient(testToken, WithBaseURL(srv.URL))
	if err := client.Send(t.Context(), testMessage()); err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	if _, ok := raw["parse_mode"]; ok {
		t.Error("request carries parse_mode")
	}
}

func TestSendSurfacesAPIErrors(t *testing.T) {
	tests := map[string]struct {
		status        int
		body          string
		wantCode      int
		wantRetryable bool
		wantRetryIn   time.Duration
	}{
		"blocked by the customer": {
			status:        http.StatusForbidden,
			body:          `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`,
			wantCode:      403,
			wantRetryable: false,
		},
		"rejected token": {
			status:        http.StatusUnauthorized,
			body:          `{"ok":false,"error_code":401,"description":"Unauthorized"}`,
			wantCode:      401,
			wantRetryable: false,
		},
		"rate limited": {
			status:        http.StatusTooManyRequests,
			body:          `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
			wantCode:      429,
			wantRetryable: true,
			wantRetryIn:   7 * time.Second,
		},
		"telegram outage": {
			status:        http.StatusBadGateway,
			body:          `{"ok":false,"error_code":502,"description":"Bad Gateway"}`,
			wantCode:      502,
			wantRetryable: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := NewClient(testToken, WithBaseURL(srv.URL))
			err := client.Send(t.Context(), testMessage())
			if err == nil {
				t.Fatal("Send() succeeded on a rejected call")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *APIError", err)
			}
			if apiErr.Code != tt.wantCode {
				t.Errorf("code = %d, want %d", apiErr.Code, tt.wantCode)
			}
			if apiErr.Retryable() != tt.wantRetryable {
				t.Errorf("Retryable() = %v, want %v", apiErr.Retryable(), tt.wantRetryable)
			}
			if apiErr.RetryAfter != tt.wantRetryIn {
				t.Errorf("RetryAfter = %s, want %s", apiErr.RetryAfter, tt.wantRetryIn)
			}
		})
	}
}

// TestTransportErrorsNeverDiscloseTheToken is the reason redactedError exists:
// Telegram carries the bot token in the request path, so a transport failure
// puts the credential into an error that is then logged.
func TestTransportErrorsNeverDiscloseTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close() // nothing is listening now, so the request fails at the transport

	client := NewClient(testToken, WithBaseURL(baseURL))
	err := client.Send(t.Context(), testMessage())
	if err == nil {
		t.Fatal("Send() succeeded against a closed server")
	}

	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the bot token leaked into an error: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("error %v does not show where the token was removed", err)
	}
}

// TestRedactedErrorsStayInspectable checks that hiding the token does not cost
// the ability to test what actually went wrong.
func TestRedactedErrorsStayInspectable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	client := NewClient(testToken, WithBaseURL(srv.URL))
	err := client.Send(ctx, testMessage())
	if err == nil {
		t.Fatal("Send() succeeded despite the deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false for %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the bot token leaked into an error: %v", err)
	}
}

func TestSendRejectsForeignProviders(t *testing.T) {
	msg := testMessage()
	msg.Provider = messaging.ProviderWhatsApp

	client := NewClient(testToken, WithBaseURL("http://127.0.0.1:1"))
	if err := client.Send(t.Context(), msg); err == nil {
		t.Fatal("the telegram client accepted a whatsapp message")
	}
}

func TestSendRejectsUndeliverableMessages(t *testing.T) {
	msg := testMessage()
	msg.Text = ""

	client := NewClient(testToken, WithBaseURL("http://127.0.0.1:1"))
	err := client.Send(t.Context(), msg)
	if !errors.Is(err, messaging.ErrInvalidEnvelope) {
		t.Errorf("error = %v, want ErrInvalidEnvelope", err)
	}
}

func TestSetWebhookRegistersTheEndpoint(t *testing.T) {
	var (
		gotPath string
		gotBody setWebhookRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	client := NewClient(testToken, WithBaseURL(srv.URL))
	err := client.SetWebhook(t.Context(), "https://example.run.app/webhooks/telegram", "s3cret")
	if err != nil {
		t.Fatalf("SetWebhook() returned error: %v", err)
	}

	if want := "/bot" + testToken + "/setWebhook"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody.URL != "https://example.run.app/webhooks/telegram" {
		t.Errorf("url = %q", gotBody.URL)
	}
	if gotBody.SecretToken != "s3cret" {
		t.Errorf("secret_token = %q, want s3cret", gotBody.SecretToken)
	}
	if len(gotBody.AllowedUpdates) != 1 || gotBody.AllowedUpdates[0] != "message" {
		t.Errorf("allowed_updates = %v, want [message]", gotBody.AllowedUpdates)
	}
}

// TestUnreadableResponsesBecomeErrors covers a proxy or captive portal
// answering with HTML where JSON was expected.
func TestUnreadableResponsesBecomeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	client := NewClient(testToken, WithBaseURL(srv.URL))
	err := client.Send(t.Context(), testMessage())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if !apiErr.Retryable() {
		t.Error("a 502 was classified as permanent")
	}
}
