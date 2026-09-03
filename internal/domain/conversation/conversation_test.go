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

func TestWaitingForHumanLongerThan(t *testing.T) {
	const timeout = 2 * time.Hour

	tests := map[string]struct {
		conv Conversation
		now  time.Time
		want bool
	}{
		"the assistant is answering": {
			conv: Conversation{State: StateAssistantActive, HandoffAt: at},
			now:  at.Add(9 * time.Hour),
		},
		"a colleague is already handling it": {
			conv: Conversation{State: StateHumanActive, HandoffAt: at},
			now:  at.Add(9 * time.Hour),
		},
		"the request is still fresh": {
			conv: Conversation{State: StateHumanRequested, HandoffAt: at},
			now:  at.Add(30 * time.Minute),
		},
		"nobody came": {
			conv: Conversation{State: StateHumanRequested, HandoffAt: at},
			now:  at.Add(3 * time.Hour),
			want: true,
		},

		// Records written before handovers were timed carry no start time. They
		// can only have come from the behaviour that left customers waiting on
		// nobody, so they resume rather than staying stuck forever.
		"stored before handovers were timed": {
			conv: Conversation{State: StateHumanRequested},
			now:  at,
			want: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.conv.WaitingForHumanLongerThan(timeout, tt.now); got != tt.want {
				t.Errorf("WaitingForHumanLongerThan() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHandoffIsTimed records when a colleague was asked for, separately from
// UpdatedAt, which moves on every message and so cannot answer how long a
// customer has been waiting.
func TestHandoffIsTimed(t *testing.T) {
	conv := Conversation{State: StateAssistantActive}

	if err := conv.TransitionTo(StateHumanRequested, at); err != nil {
		t.Fatalf("TransitionTo() returned error: %v", err)
	}
	if !conv.HandoffAt.Equal(at) {
		t.Errorf("HandoffAt = %s, want %s", conv.HandoffAt, at)
	}

	// Handing back and forth re-times the wait rather than keeping the first.
	later := at.Add(4 * time.Hour)
	if err := conv.TransitionTo(StateAssistantActive, later); err != nil {
		t.Fatalf("TransitionTo() returned error: %v", err)
	}
	if err := conv.TransitionTo(StateHumanRequested, later); err != nil {
		t.Fatalf("TransitionTo() returned error: %v", err)
	}
	if !conv.HandoffAt.Equal(later) {
		t.Errorf("HandoffAt = %s, want the second request at %s", conv.HandoffAt, later)
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
