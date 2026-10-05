package notifications

import (
	"reflect"
	"testing"
)

func TestPlanRequiresVerifiedAssociationConsentAndApprovedTemplate(t *testing.T) {
	phone := "+37494768067"
	policy := Policy{Enabled: map[Channel]bool{Telegram: true, WhatsApp: true, SMS: true},
		Templates: map[Purpose]string{BookingCreated: "booking_created_hy"}}
	recipient := Recipient{Phone: "094768067", TelegramChat: "chat-42", TelegramPhone: phone,
		WhatsAppOptedIn: true, WhatsAppOptInPhone: phone}
	for _, tt := range []struct {
		name   string
		change func(*Policy, *Recipient)
		want   []Channel
	}{
		{"default priority", func(*Policy, *Recipient) {}, []Channel{Telegram, WhatsApp, SMS}},
		{"WhatsApp first", func(p *Policy, _ *Recipient) { p.MessengerOrder = []Channel{WhatsApp, Telegram} }, []Channel{WhatsApp, Telegram, SMS}},
		{"unknown messenger client", func(_ *Policy, r *Recipient) { r.TelegramPhone = ""; r.WhatsAppOptedIn = false }, []Channel{SMS}},
		{"different phone association", func(_ *Policy, r *Recipient) { r.TelegramPhone = "+37491123456"; r.WhatsAppOptInPhone = "+37491123456" }, []Channel{SMS}},
		{"WhatsApp paused", func(p *Policy, _ *Recipient) { p.Enabled[WhatsApp] = false }, []Channel{Telegram, SMS}},
		{"template unavailable", func(p *Policy, _ *Recipient) { p.Templates = nil }, []Channel{Telegram, SMS}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := policy
			p.Enabled = map[Channel]bool{Telegram: true, WhatsApp: true, SMS: true}
			r := recipient
			tt.change(&p, &r)
			targets, err := Plan(p, r, BookingCreated)
			if err != nil {
				t.Fatal(err)
			}
			var got []Channel
			for _, target := range targets {
				got = append(got, target.Channel)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("channels=%v want=%v", got, tt.want)
			}
			if targets[len(targets)-1].Address != phone {
				t.Error("SMS phone was not normalized")
			}
		})
	}
}

func TestVerificationStaysOnNativeSMSRoute(t *testing.T) {
	phone := "+37494768067"
	targets, err := Plan(Policy{Enabled: map[Channel]bool{Telegram: true, WhatsApp: true, SMS: true},
		Templates: map[Purpose]string{Verification: "otp"}}, Recipient{Phone: phone,
		TelegramChat: "chat-42", TelegramPhone: phone, WhatsAppOptedIn: true, WhatsAppOptInPhone: phone}, Verification)
	if err != nil || !reflect.DeepEqual(targets, []Target{{Channel: SMS, Address: phone}}) {
		t.Fatalf("targets=%v err=%v", targets, err)
	}
}

func TestInvalidPolicyAndPhoneCannotProduceSendPlan(t *testing.T) {
	for _, order := range [][]Channel{{SMS, Telegram}, {Telegram, Telegram}, {"instagram"}} {
		if _, err := Plan(Policy{MessengerOrder: order}, Recipient{Phone: "+37494768067"}, BookingCreated); err == nil {
			t.Errorf("accepted invalid order %v", order)
		}
	}
	if _, err := Plan(Policy{}, Recipient{Phone: "name instead of phone"}, BookingCreated); err == nil {
		t.Error("accepted invalid phone")
	}
}
