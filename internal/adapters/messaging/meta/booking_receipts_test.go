package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBookingReceiptRequiresSignatureAndBusinessNumber(t *testing.T) {
	for _, test := range []struct {
		name, business, recipient, normalized string
		signed                                bool
		calls                                 int
	}{
		{"Armenia", "work-number", "37495152507", "+37495152507", true, 1},
		{"Russia", "work-number", "79161234567", "+79161234567", true, 1},
		{"United States", "work-number", "12025550123", "+12025550123", true, 1},
		{"United Kingdom", "work-number", "442079460018", "+442079460018", true, 1},
		{"explicit country code", "work-number", "+79161234567", "+79161234567", true, 1},
		{"forged", "work-number", "79161234567", "", false, 0},
		{"other number", "other-number", "79161234567", "", true, 0},
		{"malformed", "work-number", "redacted", "", true, 0},
		{"empty", "work-number", "", "", true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			webhook, _ := NewWebhook("app-secret", "verify-token")
			h := NewWhatsAppHandler(webhook, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "work-number").WithBookingStatuses("work-number", func(_ context.Context, reference, phone, state string) error {
				calls++
				if reference != "native-booking:event" || phone != test.normalized || state != "failed" {
					t.Fatal("receipt fields changed")
				}
				return nil
			})
			body := `{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"` + test.business + `"},"statuses":[{"biz_opaque_callback_data":"native-booking:event","recipient_id":"` + test.recipient + `","status":"failed"}]}}]}]}`
			req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/whatsapp", strings.NewReader(body))
			if test.signed {
				mac := hmac.New(sha256.New, []byte("app-secret"))
				mac.Write([]byte(body))
				req.Header.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if calls != test.calls {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
