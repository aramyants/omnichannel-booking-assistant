package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const (
	testAppSecret   = "app-secret-value"
	testVerifyToken = "verify-token-value"
)

var receivedAt = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func testWebhook(t *testing.T) *Webhook {
	t.Helper()

	w, err := NewWebhook(testAppSecret, testVerifyToken)
	if err != nil {
		t.Fatalf("NewWebhook() returned error: %v", err)
	}
	return w
}

// sign produces the header Meta would send for this body.
func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testAppSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type recordingHandler struct {
	got []messaging.Envelope
	err error
}

func (h *recordingHandler) Handle(_ context.Context, msg messaging.Envelope) error {
	h.got = append(h.got, msg)
	return h.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newHandler(messages MessageHandler, t *testing.T) *Handler {
	h := NewWhatsAppHandler(testWebhook(t), messages, discardLogger())
	h.now = func() time.Time { return receivedAt }
	return h
}

func post(t *testing.T, h http.Handler, signature string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/whatsapp",
		strings.NewReader(string(body)))
	if signature != "" {
		req.Header.Set(SignatureHeader, signature)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestSubscriptionHandshake covers the GET that proves the endpoint belongs to
// whoever configured the Meta app.
func TestSubscriptionHandshake(t *testing.T) {
	h := newHandler(&recordingHandler{}, t)

	query := url.Values{
		"hub.mode":         {"subscribe"},
		"hub.verify_token": {testVerifyToken},
		"hub.challenge":    {"1158201444"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/webhooks/whatsapp?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// Meta accepts the subscription only if the challenge comes back verbatim.
	if got := rec.Body.String(); got != "1158201444" {
		t.Errorf("body = %q, want the challenge echoed", got)
	}
}

func TestSubscriptionIsRefusedWithoutTheRightToken(t *testing.T) {
	h := newHandler(&recordingHandler{}, t)

	for name, query := range map[string]url.Values{
		"wrong token": {
			"hub.mode": {"subscribe"}, "hub.verify_token": {"guessed"},
			"hub.challenge": {"1158201444"},
		},
		"no token": {
			"hub.mode": {"subscribe"}, "hub.challenge": {"1158201444"},
		},
		"not a subscription": {
			"hub.mode": {"unsubscribe"}, "hub.verify_token": {testVerifyToken},
			"hub.challenge": {"1158201444"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/webhooks/whatsapp?"+query.Encode(), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			if strings.Contains(rec.Body.String(), "1158201444") {
				t.Error("the challenge was echoed to an unverified caller")
			}
		})
	}
}

// TestSignatureIsRequired is the endpoint's only authentication for deliveries.
func TestSignatureIsRequired(t *testing.T) {
	body := fixture(t, "whatsapp_text.json")

	for name, signature := range map[string]string{
		"no signature":      "",
		"wrong signature":   "sha256=" + strings.Repeat("00", 32),
		"not hex":           "sha256=zzzz",
		"missing prefix":    strings.TrimPrefix(sign(body), "sha256="),
		"signature of else": sign([]byte(`{"object":"something else"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			messages := &recordingHandler{}
			rec := post(t, newHandler(messages, t), signature, body)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if len(messages.got) != 0 {
				t.Error("an unsigned delivery reached the application")
			}
		})
	}
}

func TestWhatsAppTextMessage(t *testing.T) {
	messages := &recordingHandler{}
	body := fixture(t, "whatsapp_text.json")

	rec := post(t, newHandler(messages, t), sign(body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 1 {
		t.Fatalf("handled %d messages, want 1", len(messages.got))
	}

	got := messages.got[0]
	want := messaging.Envelope{
		Provider:          messaging.ProviderWhatsApp,
		ExternalMessageID: "wamid.HBgLMzc0OTUxNTI1MDcVAgASGCA5QjE2",
		ExternalUserID:    "37495152507",
		ExternalThreadID:  "37495152507",
		SentAt:            time.Unix(1756728000, 0).UTC(),
		ReceivedAt:        receivedAt,
		Sender:            messaging.Sender{DisplayName: "Anna Petrosyan"},
		Content: messaging.Content{
			Type: messaging.ContentTypeText,
			Text: "Barev, kuzenayi gerandzvel",
		},
	}

	if got != want {
		t.Errorf("envelope mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestDeliveryReceiptsAreNotMessages: read and delivered receipts arrive on the
// same webhook as real messages. Treating one as a customer writing in would
// have the assistant answer nobody.
func TestDeliveryReceiptsAreNotMessages(t *testing.T) {
	messages := &recordingHandler{}
	body := fixture(t, "whatsapp_status.json")

	rec := post(t, newHandler(messages, t), sign(body), body)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Errorf("a delivery receipt was handled as a message: %+v", messages.got)
	}
}

func TestWhatsAppVoiceMessageIsDescribed(t *testing.T) {
	messages := &recordingHandler{}
	body := fixture(t, "whatsapp_voice.json")

	if rec := post(t, newHandler(messages, t), sign(body), body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(messages.got) != 1 {
		t.Fatalf("handled %d messages, want 1", len(messages.got))
	}

	content := messages.got[0].Content
	if content.Type != messaging.ContentTypeUnsupported {
		t.Errorf("type = %q, want unsupported", content.Type)
	}
	if content.Description != "voice message" {
		t.Errorf("description = %q, want it to name what arrived", content.Description)
	}
}

func TestUnparseableDeliveryIsAcknowledged(t *testing.T) {
	messages := &recordingHandler{}
	body := []byte("{not json")

	rec := post(t, newHandler(messages, t), sign(body), body)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d so Meta stops redelivering", rec.Code, http.StatusOK)
	}
}

func TestProcessingFailureAsksForRedelivery(t *testing.T) {
	messages := &recordingHandler{err: context.DeadlineExceeded}
	body := fixture(t, "whatsapp_text.json")

	rec := post(t, newHandler(messages, t), sign(body), body)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d so Meta redelivers", rec.Code, http.StatusInternalServerError)
	}
}

func TestSendWhatsApp(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		sent    sendTextRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT"}]}`))
	}))
	defer srv.Close()

	client, err := NewClient("access-token", "106540352242922",
		WithBaseURL(srv.URL), WithGraphVersion("v22.0"))
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}

	err = client.Send(t.Context(), messaging.Outgoing{
		Provider:         messaging.ProviderWhatsApp,
		ExternalThreadID: "37495152507",
		Text:             "Բարև",
	})
	if err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}

	if want := "/v22.0/106540352242922/messages"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	// The token must not travel in the query string, where it would be written
	// to every access log on the way.
	if gotAuth != "Bearer access-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if sent.MessagingProduct != "whatsapp" || sent.To != "37495152507" {
		t.Errorf("payload = %+v", sent)
	}
	if sent.Text.Body != "Բարև" {
		t.Errorf("body = %q", sent.Text.Body)
	}
	if sent.Text.PreviewURL {
		t.Error("link previews are on; a preview card is noise in a booking conversation")
	}
}

// TestOutsideTheServiceWindowIsItsOwnFailure: WhatsApp refuses a free-form
// message more than 24 hours after the customer last wrote. Retrying cannot
// help, so it must not look like a transient fault.
func TestOutsideTheServiceWindowIsItsOwnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Message failed to send","code":131047,
			"error_user_msg":"More than 24 hours have passed since the recipient last replied."}}`))
	}))
	defer srv.Close()

	client, _ := NewClient("access-token", "106540352242922", WithBaseURL(srv.URL))
	err := client.Send(t.Context(), messaging.Outgoing{
		Provider:         messaging.ProviderWhatsApp,
		ExternalThreadID: "37495152507",
		Text:             "hello",
	})

	if !errorsIs(err, ErrOutsideServiceWindow) {
		t.Errorf("error = %v, want ErrOutsideServiceWindow", err)
	}
}

func TestGraphFailuresAreClassified(t *testing.T) {
	tests := map[string]struct {
		status int
		want   error
	}{
		"rate limited": {http.StatusTooManyRequests, ErrUnavailable},
		"meta outage":  {http.StatusBadGateway, ErrUnavailable},
		"bad token":    {http.StatusUnauthorized, ErrRejected},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			}))
			defer srv.Close()

			client, _ := NewClient("access-token", "1", WithBaseURL(srv.URL))
			err := client.Send(t.Context(), messaging.Outgoing{
				Provider:         messaging.ProviderWhatsApp,
				ExternalThreadID: "37495152507",
				Text:             "hello",
			})
			if !errorsIs(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewClientRequiresAToken(t *testing.T) {
	if _, err := NewClient("", "1"); err == nil {
		t.Error("NewClient() accepted an empty access token")
	}
}

func TestNewWebhookRequiresBothSecrets(t *testing.T) {
	if _, err := NewWebhook("", testVerifyToken); err == nil {
		t.Error("NewWebhook() accepted an empty app secret")
	}
	if _, err := NewWebhook(testAppSecret, ""); err == nil {
		t.Error("NewWebhook() accepted an empty verify token")
	}
}
func errorsIs(err, target error) bool { return errors.Is(err, target) }
