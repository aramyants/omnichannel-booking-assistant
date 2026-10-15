package assistant

import (
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func openConversationFor(t *testing.T, store *memory.Store, msg messaging.Envelope) conversation.Conversation {
	t.Helper()
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{ID: "unused", Provider: msg.Provider, ExternalThreadID: msg.ExternalThreadID})
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestNewBookingRequiresSMSProofAndFreshConfirmation(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger} {
		t.Run(string(provider), func(t *testing.T) {
			sender, sms, calendar := &fakeSender{}, &cabinetTestSMS{}, defaultScheduling()
			model := &scriptedAI{responses: []ai.Response{prepareCall("prepare")}}
			svc, store := newAIService(t, model, calendar, sender)
			proof := &cabinet.Identity{Repo: store, Sender: sms, Secret: []byte(strings.Repeat("s", 32)), Now: func() time.Time { return testNow }}
			svc.identity, svc.tools.identity, svc.senders[provider] = proof, proof, sender
			msg := incoming("proposal")
			msg.Provider, msg.Sender.Language = provider, "en"
			if err := svc.Handle(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			conv := openConversationFor(t, store, msg)
			if len(calendar.created) != 0 || conv.Draft == nil || !conv.Draft.AwaitingPhoneVerification || sms.code != "" {
				t.Fatal("unverified proposal created a booking, lost its selection, or sent an unsolicited code")
			}
			if !strings.Contains(sender.sent[len(sender.sent)-1].Text, "Before booking") {
				t.Fatal("missing verification explanation")
			}
			for _, step := range []struct{ id, text string }{{"premature-confirm", "yes"}, {"sms-request", "+37411223344"}} {
				msg.ExternalMessageID, msg.Content.Text = step.id, step.text
				if err := svc.Handle(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
			}
			if sms.code == "" || len(calendar.created) != 0 {
				t.Fatal("SMS was not requested, or an unproved booking was created")
			}
			msg.ExternalMessageID, msg.Content.Text = "proof", sms.code
			if err := svc.Handle(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			conv = openConversationFor(t, store, msg)
			if len(calendar.created) != 0 || conv.Draft == nil || conv.Draft.AwaitingPhoneVerification || conv.Draft.PreparedFromMessageID != "proof" {
				t.Fatal("code was used as booking consent or the matching selection was lost")
			}
			text := sender.sent[len(sender.sent)-1].Text
			if !strings.Contains(text, "Nothing is booked yet") || !strings.Contains(text, "Mariam") || !strings.Contains(text, "+37411223344") {
				t.Fatalf("missing fresh confirmation summary: %s", text)
			}
			messages, err := store.Recent(t.Context(), conv.ID, 30)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range messages {
				if strings.Contains(m.Text, sms.code) {
					t.Fatal("code entered the transcript")
				}
			}
			msg.ExternalMessageID, msg.Content.Text = "confirmed-after-proof", "yes"
			if err := svc.Handle(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			if len(calendar.created) != 1 || calendar.created[0].Phone != "+37411223344" {
				t.Fatal("verified confirmation did not create exactly the selected booking")
			}
		})
	}
}

func TestTelegramBookingSelectionSurvivesVerifiedProfileMerge(t *testing.T) {
	sender, calendar := &fakeSender{}, defaultScheduling()
	svc, store := newAIService(t, &scriptedAI{responses: []ai.Response{prepareCall("prepare")}}, calendar, sender)
	proof := &cabinet.Identity{Repo: store, Now: func() time.Time { return testNow }}
	svc.identity, svc.tools.identity = proof, proof
	msg := incoming("proposal")
	msg.Sender.Language = "en"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if _, err := proof.AcceptTelegramContact(t.Context(), msg.ExternalUserID, "+37411223344", "contact"); err != nil {
		t.Fatal(err)
	}
	// The Telegram adapter resumes after accepting the authenticated own contact.
	msg.ExternalMessageID, msg.Content.Text = "contact", "/appointments"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	conv := openConversation(t, store)
	if conv.Draft == nil || conv.Draft.AwaitingPhoneVerification || len(calendar.created) != 0 {
		t.Fatal("profile merge lost the verified selection or silently booked it")
	}
	msg.ExternalMessageID, msg.Content.Text = "confirmed", "yes"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(calendar.created) != 1 {
		t.Fatal("verified own-contact booking did not complete")
	}
}

func TestBookingProofMustMatchExactPhoneAndProtectOldDrafts(t *testing.T) {
	for _, phone := range []string{"37411223344", "37411999999"} {
		t.Run(phone, func(t *testing.T) {
			sender, calendar := &fakeSender{}, defaultScheduling()
			svc, store := newAIService(t, &scriptedAI{responses: []ai.Response{prepareCall("prepare"), textResponse("Please confirm.")}}, calendar, sender)
			msg := incoming("proposal")
			msg.Provider, msg.ExternalUserID, msg.ExternalThreadID, msg.Sender.Language = messaging.ProviderWhatsApp, phone, phone, "en"
			svc.senders[msg.Provider] = sender
			proof := &cabinet.Identity{Repo: store, Now: func() time.Time { return testNow }}
			svc.identity = proof
			// Persist an older draft before enabling the proof requirement.
			if err := svc.Handle(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			svc.identity, svc.tools.identity = proof, proof
			msg.ExternalMessageID, msg.Content.Text = "confirmed", "yes"
			if err := svc.Handle(t.Context(), msg); err != nil {
				t.Fatal(err)
			}
			if phone == "37411223344" && len(calendar.created) != 1 {
				t.Fatal("authenticated matching WhatsApp phone was unnecessarily blocked")
			}
			if phone != "37411223344" && len(calendar.created) != 0 {
				t.Fatal("a different authenticated phone bypassed the booking proof")
			}
		})
	}
}

func TestDifferentSMSPhoneCannotResumeSelectedBooking(t *testing.T) {
	sender, sms, calendar := &fakeSender{}, &cabinetTestSMS{}, defaultScheduling()
	svc, store := newAIService(t, &scriptedAI{responses: []ai.Response{prepareCall("prepare")}}, calendar, sender)
	proof := &cabinet.Identity{Repo: store, Sender: sms, Secret: []byte(strings.Repeat("s", 32)), Now: func() time.Time { return testNow }}
	svc.identity, svc.tools.identity = proof, proof
	msg := incoming("proposal")
	msg.Provider, msg.Sender.Language = messaging.ProviderInstagram, "en"
	svc.senders[msg.Provider] = sender
	for _, step := range []struct{ id, text string }{{"proposal", msg.Content.Text}, {"other-phone", "+37411999999"}} {
		msg.ExternalMessageID, msg.Content.Text = step.id, step.text
		if err := svc.Handle(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	msg.ExternalMessageID, msg.Content.Text = "other-phone-proof", sms.code
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(calendar.created) != 0 || openConversationFor(t, store, msg).Draft != nil {
		t.Fatal("proof for a different phone resumed or created the selected booking")
	}
}

func TestUnavailableSMSCannotCreateBooking(t *testing.T) {
	sender, calendar := &fakeSender{}, defaultScheduling()
	svc, store := newAIService(t, &scriptedAI{responses: []ai.Response{prepareCall("prepare")}}, calendar, sender)
	proof := &cabinet.Identity{Repo: store, Now: func() time.Time { return testNow }}
	svc.identity, svc.tools.identity = proof, proof
	msg := incoming("proposal")
	msg.Provider, msg.Sender.Language = messaging.ProviderInstagram, "en"
	svc.senders[msg.Provider] = sender
	for _, step := range []struct{ id, text string }{{"proposal", msg.Content.Text}, {"phone", "+37411223344"}, {"premature-confirm", "yes"}} {
		msg.ExternalMessageID, msg.Content.Text = step.id, step.text
		if err := svc.Handle(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if len(calendar.created) != 0 {
		t.Fatal("SMS unavailability bypassed ownership proof")
	}
}
