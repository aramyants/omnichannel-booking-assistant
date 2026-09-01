package conversation

import (
	"errors"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

var at = time.Unix(1756728000, 0).UTC()

func TestAssistantMayReplyOnlyWhenItIsActive(t *testing.T) {
	tests := map[State]bool{
		StateAssistantActive: true,
		StateHumanRequested:  false,
		StateHumanActive:     false,
		StateClosed:          false,
	}

	for state, want := range tests {
		conv := Conversation{State: state}
		if got := conv.AssistantMayReply(); got != want {
			t.Errorf("AssistantMayReply() = %v for state %q, want %v", got, state, want)
		}
	}
}

func TestTransitions(t *testing.T) {
	tests := map[string]struct {
		from    State
		to      State
		wantErr bool
	}{
		"a customer asks for a person":     {from: StateAssistantActive, to: StateHumanRequested},
		"a colleague picks the request up": {from: StateHumanRequested, to: StateHumanActive},
		"a colleague hands back":           {from: StateHumanActive, to: StateAssistantActive},
		"a request is withdrawn":           {from: StateHumanRequested, to: StateAssistantActive},
		"a conversation ends":              {from: StateAssistantActive, to: StateClosed},
		"a finished conversation reopens":  {from: StateClosed, to: StateAssistantActive},

		// A finished conversation cannot jump straight to a colleague without
		// being reopened first, which is what records that it is live again.
		"closed to human active":         {from: StateClosed, to: StateHumanActive, wantErr: true},
		"closed to human requested":      {from: StateClosed, to: StateHumanRequested, wantErr: true},
		"human active back to requested": {from: StateHumanActive, to: StateHumanRequested, wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			conv := Conversation{State: tt.from}
			err := conv.TransitionTo(tt.to, at)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("TransitionTo() allowed %s to %s", tt.from, tt.to)
				}
				if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("error %v does not wrap ErrInvalidTransition", err)
				}
				if conv.State != tt.from {
					t.Errorf("a refused transition still changed the state to %q", conv.State)
				}
				return
			}

			if err != nil {
				t.Fatalf("TransitionTo() refused %s to %s: %v", tt.from, tt.to, err)
			}
			if conv.State != tt.to {
				t.Errorf("state = %q, want %q", conv.State, tt.to)
			}
			if !conv.UpdatedAt.Equal(at) {
				t.Errorf("UpdatedAt = %s, want %s", conv.UpdatedAt, at)
			}
		})
	}
}

// TestTransitionToTheCurrentStateIsAllowed means a caller reacting to a
// repeated request does not have to check the state first.
func TestTransitionToTheCurrentStateIsAllowed(t *testing.T) {
	conv := Conversation{State: StateHumanActive}
	if err := conv.TransitionTo(StateHumanActive, at); err != nil {
		t.Errorf("TransitionTo() refused a no-op: %v", err)
	}
}

func TestKeyIsUniquePerChannelThread(t *testing.T) {
	telegram := Conversation{Provider: messaging.ProviderTelegram, ExternalThreadID: "12345"}
	whatsapp := Conversation{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "12345"}

	if telegram.Key() == whatsapp.Key() {
		t.Errorf("a matching thread id on two channels collided on key %q", telegram.Key())
	}
	if telegram.Key() != Key(messaging.ProviderTelegram, "12345") {
		t.Error("Key() and the method disagree")
	}
}
