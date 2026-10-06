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
		name, phone string
		signed      bool
		calls       int
	}{
		{"valid", "work-number", true, 1}, {"forged", "work-number", false, 0}, {"other number", "other-number", true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			webhook, _ := NewWebhook("app-secret", "verify-token")
			h := NewWhatsAppHandler(webhook, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "work-number").WithBookingStatuses("work-number", func(_ context.Context, reference, phone, state string) error {
				calls++
				if reference != "native-booking:event" || phone != "37495152507" || state != "failed" {
					t.Fatal("receipt fields changed")
				}
				return nil
			})
			body := `{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"` + test.phone + `"},"statuses":[{"biz_opaque_callback_data":"native-booking:event","recipient_id":"37495152507","status":"failed"}]}}]}]}`
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
