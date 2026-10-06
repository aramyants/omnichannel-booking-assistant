package assistant

import (
	"context"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"strings"
	"testing"
	"time"
)

type profileScheduling struct {
	*stubScheduling
	phones     []string
	native     []booking.Booking
	historyErr error
}

func (s *profileScheduling) ListPhoneBookings(_ context.Context, phone string, _ time.Time) ([]booking.Booking, error) {
	s.phones = append(s.phones, phone)
	return s.native, s.historyErr
}

type cabinetTestSMS struct{ code string }

func (s *cabinetTestSMS) SendCode(_ context.Context, _ string, text, _ string, _ time.Time) error {
	s.code = strings.Split(text, " ")[2]
	return nil
}

func TestFormBookingHistoryAcrossVerifiedChannels(t *testing.T) {
	providers := []messaging.Provider{messaging.ProviderWhatsApp, messaging.ProviderTelegram, messaging.ProviderInstagram, messaging.ProviderMessenger}
	sender := &fakeSender{}
	calendar := &profileScheduling{stubScheduling: defaultScheduling(), native: []booking.Booking{
		{ExternalID: "form-123", StartsAt: testNow.Add(time.Hour), Status: booking.StatusConfirmed, ServiceNames: []string{"Back Motion"}, StaffName: "Garik"},
		{ExternalID: "cancelled-456", StartsAt: testNow.Add(time.Hour), Status: booking.StatusCancelled},
	}}
	svc, store := newAIService(t, nil, calendar, sender)
	identity := &cabinet.Identity{Repo: store, Secret: []byte(strings.Repeat("s", 32)), Now: func() time.Time { return testNow }}
	svc.identity = identity
	svc.tools.identity = identity
	sms := &cabinetTestSMS{}
	identity.Sender = sms
	canonical := ""
	for _, provider := range providers {
		user := string(provider) + "-client"
		if provider == messaging.ProviderWhatsApp {
			user = "37411223344"
		}
		if provider == messaging.ProviderTelegram {
			if err := cabinet.RecordTelegramResolution(t.Context(), store, user, "+37411223344", testNow); err != nil {
				t.Fatal(err)
			}
		}
		if provider == messaging.ProviderInstagram || provider == messaging.ProviderMessenger {
			_ = identity.Begin(t.Context(), provider, user)
			// Different phone budget entries still respect a minute between SMS sends.
			identity.Now = func() time.Time { return testNow.Add(time.Duration(len(calendar.phones)) * time.Minute) }
			if err := identity.Send(t.Context(), provider, user, "+37411223344"); err != nil {
				t.Fatal(err)
			}
			if _, err := identity.Confirm(t.Context(), provider, user, sms.code); err != nil {
				t.Fatal(err)
			}
		}
		svc.senders[provider] = sender
		msg := incomingText("history-"+string(provider), "/appointments")
		msg.Provider = provider
		msg.ExternalUserID = user
		msg.ExternalThreadID = user
		if err := svc.Handle(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
		reply := sender.sent[len(sender.sent)-1].Text
		if !strings.Contains(reply, "form-123") || strings.Contains(reply, "cancelled-456") {
			t.Fatalf("%s wrong history: %s", provider, reply)
		}
		cust, err := svc.identify(t.Context(), msg, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if canonical == "" {
			canonical = cust.ID
		}
		if cust.ID != canonical {
			t.Fatal("channels resolved different client profiles")
		}
	}
	if len(calendar.phones) != 4 {
		t.Fatalf("history lookups=%d", len(calendar.phones))
	}
	for _, phone := range calendar.phones {
		if phone != "+37411223344" {
			t.Fatal("history not scoped to verified phone")
		}
	}
}

func TestTypedPhoneNeverGrantsHistoryAndVerificationCodeStaysOutOfTranscript(t *testing.T) {
	sender := &fakeSender{}
	calendar := &profileScheduling{stubScheduling: defaultScheduling(), native: []booking.Booking{{ExternalID: "secret", StartsAt: testNow.Add(time.Hour), Status: booking.StatusConfirmed}}}
	svc, store := newAIService(t, nil, calendar, sender)
	sms := &cabinetTestSMS{}
	identity := &cabinet.Identity{Repo: store, Sender: sms, Secret: []byte(strings.Repeat("s", 32)), Now: func() time.Time { return testNow }}
	svc.identity = identity
	svc.tools.identity = identity
	svc.senders[messaging.ProviderInstagram] = sender
	msg := incomingText("typed", "+37411223344")
	msg.Provider = messaging.ProviderInstagram
	msg.ExternalUserID = "alice"
	msg.ExternalThreadID = "alice"
	msg.Sender.Language = "en"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	msg.ExternalMessageID = "list"
	msg.Content.Text = "/appointments"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(calendar.phones) != 0 {
		t.Fatal("typed contact exposed appointments")
	}
	msg.ExternalMessageID = "request"
	msg.Content.Text = "+37411223344"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	msg.ExternalMessageID = "code"
	msg.Content.Text = sms.code
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(calendar.phones) != 1 {
		t.Fatal("verified history not loaded")
	}
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{ID: "unused", Provider: msg.Provider, ExternalThreadID: msg.ExternalThreadID})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := store.Recent(t.Context(), conv.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if strings.Contains(m.Text, sms.code) {
			t.Fatal("OTP entered model transcript")
		}
	}
	msg.ExternalThreadID = "public-group"
	msg.Provider = messaging.ProviderTelegram
	before := len(sender.sent)
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != before {
		t.Fatal("private history answered a public group")
	}
}
func TestUnavailableHistoryNeverClaimsNoAppointments(t *testing.T) {
	sender := &fakeSender{}
	calendar := &profileScheduling{stubScheduling: defaultScheduling(), historyErr: errors.New("provider unavailable")}
	svc, store := newAIService(t, nil, calendar, sender)
	identity := &cabinet.Identity{Repo: store}
	svc.identity = identity
	svc.tools.identity = identity
	svc.senders[messaging.ProviderWhatsApp] = sender
	msg := incomingText("failure", "/appointments")
	msg.Provider = messaging.ProviderWhatsApp
	msg.ExternalUserID = "37411223344"
	msg.ExternalThreadID = msg.ExternalUserID
	msg.Sender.Language = "en"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	text := sender.sent[0].Text
	if strings.Contains(text, "no upcoming") || strings.Contains(text, "nothing booked") {
		t.Fatal("provider failure became false absence")
	}
	if len(calendar.phones) != 1 {
		t.Fatalf("wrong calls: %d", len(calendar.phones))
	}
}
