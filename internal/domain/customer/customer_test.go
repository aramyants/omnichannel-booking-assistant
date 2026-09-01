package customer

import (
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestChannelIdentityKeyIsUniquePerChannel(t *testing.T) {
	telegram := ChannelIdentity{Provider: messaging.ProviderTelegram, ExternalUserID: "12345"}
	whatsapp := ChannelIdentity{Provider: messaging.ProviderWhatsApp, ExternalUserID: "12345"}

	if telegram.Key() == whatsapp.Key() {
		t.Errorf("a matching user id on two channels collided on key %q", telegram.Key())
	}
}

func TestChannelIdentityValidate(t *testing.T) {
	complete := ChannelIdentity{
		CustomerID:     "cust-1",
		Provider:       messaging.ProviderTelegram,
		ExternalUserID: "12345",
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("a complete identity was rejected: %v", err)
	}

	tests := map[string]func(*ChannelIdentity){
		"missing customer": func(c *ChannelIdentity) { c.CustomerID = "" },
		"missing provider": func(c *ChannelIdentity) { c.Provider = "" },
		"missing user id":  func(c *ChannelIdentity) { c.ExternalUserID = "" },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			identity := complete
			breakIt(&identity)

			if err := identity.Validate(); err == nil {
				t.Error("Validate() accepted an incomplete identity")
			}
		})
	}
}
