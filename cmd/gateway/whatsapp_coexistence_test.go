package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/meta"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type coexistencePipelineSpy struct {
	events []string
	reply  messaging.ExternalReply
	err    error
}

func (s *coexistencePipelineSpy) Handle(context.Context, messaging.Envelope) error {
	s.events = append(s.events, "customer")
	return nil
}
func (s *coexistencePipelineSpy) RecordExternalReply(_ context.Context, reply messaging.ExternalReply) error {
	s.events = append(s.events, "staff")
	s.reply = reply
	return s.err
}

func TestNotificationLayerPreservesSignedBusinessAppTakeover(t *testing.T) {
	for _, tc := range []struct {
		name, phone, signature string
		err                    error
		status                 int
		order                  string
	}{
		{name: "staff before customer", phone: "studio-phone", signature: "valid", status: 200, order: "staff,customer"},
		{name: "unsigned", phone: "studio-phone", status: 401},
		{name: "other asset", phone: "other-phone", signature: "valid", status: 200},
		{name: "persistence failure", phone: "studio-phone", signature: "valid", err: errors.New("store unavailable"), status: 500, order: "staff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().Unix()
			body := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"studio-phone"},"messages":[{"from":"15550000002","id":"incoming","timestamp":"%d","type":"text","text":{"body":"Thanks"}}]}},{"field":"smb_message_echoes","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"studio-phone"},"message_echoes":[{"from":"15550000001","to":"15550000002","id":"staff-reply","timestamp":"%d","type":"text","text":{"body":"Our team is checking your appointment."}}]}}]}]}`, now, now)
			spy := &coexistencePipelineSpy{err: tc.err}
			webhook, _ := meta.NewWebhook("secret", "verify")
			h := meta.NewWhatsAppHandler(webhook, notificationMessages{next: spy}, slog.New(slog.NewTextHandler(io.Discard, nil)), tc.phone)
			r := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/whatsapp", strings.NewReader(body))
			if tc.signature != "" {
				mac := hmac.New(sha256.New, []byte("secret"))
				_, _ = mac.Write([]byte(body))
				r.Header.Set(meta.SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || strings.Join(spy.events, ",") != tc.order {
				t.Fatalf("status=%d processing=%v", w.Code, spy.events)
			}
			if tc.order != "" && (spy.reply.ExternalThreadID != "15550000002" || spy.reply.ExternalMessageID != "staff-reply") {
				t.Fatal("staff activity lost its original recipient or dedupe identity")
			}
		})
	}
}

func TestNotificationLayerFailsClosedWithoutTakeoverRecorder(t *testing.T) {
	err := (notificationMessages{}).RecordExternalReply(t.Context(), messaging.ExternalReply{})
	if err == nil {
		t.Fatal("an unwired takeover recorder must not silently discard staff replies")
	}
}
