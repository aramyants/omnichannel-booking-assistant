package main

import (
	"bytes"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBookingContextRejectsExpiredUnsignedAndTamperedBodies(t *testing.T) {
	for _, mode := range []string{"unsigned", "expired", "tampered", "valid"} {
		t.Run(mode, func(t *testing.T) {
			s := &notifications.Service{Repo: memory.New()}
			handler := bookingContextHandler(s, "private-test-token", "https://book.motionconcept.rest/")
			raw := []byte(`{"action":"context","phone":"+37491123456","locale":"en","explicit":true}`)
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			if mode == "expired" {
				timestamp = strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
			}
			req := httptest.NewRequest(http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
			req.Header.Set("X-Emotion-Time", timestamp)
			signature := contextSignature("private-test-token", timestamp, raw)
			if mode == "tampered" {
				signature = strings.Repeat("0", 64)
			}
			if mode != "unsigned" {
				req.Header.Set("X-Emotion-Signature", signature)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			want := 401
			if mode == "valid" {
				want = 200
			}
			if rec.Code != want {
				t.Fatal(mode, rec.Code, rec.Body.String())
			}
		})
	}
}
