package meta

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const appEchoBody = `{"object":"whatsapp_business_account","entry":[{"id":"business-account","changes":[{"field":"smb_message_echoes","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"phone-1","display_phone_number":"15550000001"},"message_echoes":[{"from":"15550000001","to":"15550000002","id":"wamid.staff-1","timestamp":"1788436700","type":"text","text":{"body":"I am checking your appointment personally."}}]}}]}]}`

type echoRecorder struct {
	recordingHandler
	replies []messaging.ExternalReply
	events  []string
	echoErr error
}

func (r *echoRecorder) RecordExternalReply(_ context.Context, reply messaging.ExternalReply) error {
	r.replies = append(r.replies, reply)
	r.events = append(r.events, "staff")
	return r.echoErr
}

func (r *echoRecorder) Handle(ctx context.Context, msg messaging.Envelope) error {
	r.events = append(r.events, "customer")
	return r.recordingHandler.Handle(ctx, msg)
}

func TestSignedAppEchoIsAnOutboundStaffReply(t *testing.T) {
	recorder := &echoRecorder{}
	h := NewWhatsAppHandler(testWebhook(t), recorder, discardLogger(), "phone-1")
	h.now = func() time.Time { return receivedAt }
	body := []byte(appEchoBody)
	response := post(t, h, sign(body), body)
	if response.Code != http.StatusOK || len(recorder.replies) != 1 || len(recorder.got) != 0 {
		t.Fatalf("echo was not routed exclusively to staff context: %d %+v", response.Code, recorder)
	}
	reply := recorder.replies[0]
	if reply.ExternalThreadID != "15550000002" || reply.Content.Text != "I am checking your appointment personally." || reply.ExternalMessageID != "wamid.staff-1" {
		t.Fatalf("wrong recipient or content: %+v", reply)
	}
}

func TestAppEchoRequiresSignatureAndMatchingPhone(t *testing.T) {
	for _, tc := range []struct {
		name, phone, body string
		signed            bool
		want              int
	}{
		{"unsigned", "phone-1", appEchoBody, false, http.StatusUnauthorized},
		{"other phone", "phone-2", appEchoBody, true, http.StatusOK},
		{"unbound", "", appEchoBody, true, http.StatusOK},
		{"cloud API echo", "phone-1", strings.Replace(appEchoBody, "smb_message_echoes", "message_echoes", 1), true, http.StatusOK},
		{"wrong product", "phone-1", strings.Replace(appEchoBody, `"messaging_product":"whatsapp"`, `"messaging_product":"other"`, 1), true, http.StatusOK},
		{"missing recipient", "phone-1", strings.Replace(appEchoBody, `"to":"15550000002"`, `"to":""`, 1), true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &echoRecorder{}
			h := NewWhatsAppHandler(testWebhook(t), recorder, discardLogger(), tc.phone)
			signature := ""
			if tc.signed {
				signature = sign([]byte(tc.body))
			}
			response := post(t, h, signature, []byte(tc.body))
			if response.Code != tc.want || len(recorder.replies) != 0 || len(recorder.got) != 0 {
				t.Fatalf("untrusted activity processed: %d %+v", response.Code, recorder)
			}
		})
	}
}

func TestAppEchoFailureRequestsRedelivery(t *testing.T) {
	recorder := &echoRecorder{echoErr: errors.New("store unavailable")}
	h := NewWhatsAppHandler(testWebhook(t), recorder, discardLogger(), "phone-1")
	body := []byte(appEchoBody)
	if got := post(t, h, sign(body), body).Code; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want retry", got)
	}
}

func TestStaffActivityPrecedesCustomerMessagesInOneDelivery(t *testing.T) {
	body := []byte(strings.Replace(appEchoBody, `"changes":[`, `"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"phone-1"},"messages":[{"from":"15550000002","id":"incoming","type":"text","text":{"body":"Thanks"}}]}},`, 1))
	recorder := &echoRecorder{}
	h := NewWhatsAppHandler(testWebhook(t), recorder, discardLogger(), "phone-1")
	if got := post(t, h, sign(body), body).Code; got != http.StatusOK || strings.Join(recorder.events, ",") != "staff,customer" {
		t.Fatalf("wrong processing order: %d %v", got, recorder.events)
	}
}

func TestAppAudioEchoRecordsActivityWithoutAudioInput(t *testing.T) {
	body := []byte(strings.Replace(appEchoBody, `"type":"text","text":{"body":"I am checking your appointment personally."}`, `"type":"audio","audio":{"id":"media-1","mime_type":"audio/ogg"}`, 1))
	replies, err := parseWhatsAppExternalReplies(body, receivedAt, "phone-1")
	if err != nil || len(replies) != 1 || replies[0].Content.Audio != nil || replies[0].Content.Type != messaging.ContentTypeUnsupported || replies[0].Content.Text == "" {
		t.Fatalf("audio echo must only record staff activity: %+v %v", replies, err)
	}
}
