package messaging

import (
	"errors"
	"testing"
	"time"
)

func validEnvelope() Envelope {
	return Envelope{
		Provider:          ProviderTelegram,
		ExternalMessageID: "4127",
		ExternalUserID:    "219847362",
		ExternalThreadID:  "219847362",
		SentAt:            time.Unix(1756728000, 0).UTC(),
		ReceivedAt:        time.Unix(1756728001, 0).UTC(),
		Content:           Content{Type: ContentTypeText, Text: "hello"},
	}
}

func TestEnvelopeValidate(t *testing.T) {
	if err := validEnvelope().Validate(); err != nil {
		t.Fatalf("a complete envelope was rejected: %v", err)
	}

	tests := map[string]func(*Envelope){
		"missing provider":     func(e *Envelope) { e.Provider = "" },
		"missing message id":   func(e *Envelope) { e.ExternalMessageID = "" },
		"missing user id":      func(e *Envelope) { e.ExternalUserID = "" },
		"missing thread id":    func(e *Envelope) { e.ExternalThreadID = "" },
		"missing content type": func(e *Envelope) { e.Content.Type = "" },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			env := validEnvelope()
			breakIt(&env)

			err := env.Validate()
			if err == nil {
				t.Fatal("Validate() accepted an incomplete envelope")
			}
			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Errorf("error %v does not wrap ErrInvalidEnvelope", err)
			}
		})
	}
}

func TestDedupeKeyIsStablePerProviderMessage(t *testing.T) {
	first := validEnvelope()

	// The same message redelivered: provider retries change the receipt time
	// but never the provider's own message identifier.
	second := validEnvelope()
	second.ReceivedAt = second.ReceivedAt.Add(time.Minute)

	if first.DedupeKey() != second.DedupeKey() {
		t.Errorf("redelivery produced a different key: %q and %q", first.DedupeKey(), second.DedupeKey())
	}

	// The same identifier on a different channel is a different message.
	other := validEnvelope()
	other.Provider = ProviderWhatsApp
	if first.DedupeKey() == other.DedupeKey() {
		t.Errorf("two channels collided on key %q", first.DedupeKey())
	}
}

func TestReplyAddressesTheSameConversation(t *testing.T) {
	env := validEnvelope()
	reply := env.Reply("on its way")

	if reply.Provider != env.Provider {
		t.Errorf("provider = %q, want %q", reply.Provider, env.Provider)
	}
	if reply.ExternalThreadID != env.ExternalThreadID {
		t.Errorf("thread = %q, want %q", reply.ExternalThreadID, env.ExternalThreadID)
	}
	if err := reply.Validate(); err != nil {
		t.Errorf("Reply() produced an undeliverable message: %v", err)
	}
}

func TestOutgoingValidateRejectsEmptyText(t *testing.T) {
	msg := validEnvelope().Reply("")
	if err := msg.Validate(); !errors.Is(err, ErrInvalidEnvelope) {
		t.Errorf("Validate() = %v, want ErrInvalidEnvelope", err)
	}
}
