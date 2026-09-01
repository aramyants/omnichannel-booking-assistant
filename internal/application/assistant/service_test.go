package assistant

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

var testNow = time.Unix(1756728000, 0).UTC()

// fakeSender is a small stand-in rather than a generated mock: it records what
// it was asked to send, which is all these tests need to know.
type fakeSender struct {
	sent []messaging.Outgoing
	err  error
}

func (f *fakeSender) Send(_ context.Context, msg messaging.Outgoing) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

// newTestService wires the service to the in-memory store rather than to fake
// repositories, so these tests exercise the real persistence behaviour.
func newTestService(t *testing.T, sender Sender) (*Service, *memory.Store) {
	t.Helper()

	store := memory.New(memory.WithClock(func() time.Time { return testNow }))
	svc, err := NewService(Deps{
		Senders:       map[messaging.Provider]Sender{messaging.ProviderTelegram: sender},
		Customers:     store,
		Conversations: store,
		Messages:      store,
		Processed:     store,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	return svc, store
}

func incoming(messageID string) messaging.Envelope {
	return messaging.Envelope{
		Provider:          messaging.ProviderTelegram,
		ExternalMessageID: messageID,
		ExternalUserID:    "219847362",
		ExternalThreadID:  "219847362",
		SentAt:            testNow,
		ReceivedAt:        testNow,
		Sender:            messaging.Sender{DisplayName: "Anna", Language: "hy"},
		Content:           messaging.Content{Type: messaging.ContentTypeText, Text: "Can I book Friday?"},
	}
}

func TestNewServiceRequiresItsCollaborators(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Error("NewService() accepted empty dependencies")
	}
}

func TestHandleRepliesIntoTheSameConversation(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(sender.sent))
	}
	if sender.sent[0].ExternalThreadID != "219847362" {
		t.Errorf("thread = %q, want 219847362", sender.sent[0].ExternalThreadID)
	}
	if !strings.Contains(sender.sent[0].Text, "Anna") {
		t.Errorf("reply does not address the customer by name: %q", sender.sent[0].Text)
	}
}

// TestHandleIgnoresARedeliveredMessage is the point of the whole milestone.
// Every provider retries, and a retry that is handled again produces a second
// reply and, once booking exists, a second appointment.
func TestHandleIgnoresARedeliveredMessage(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	for range 3 {
		if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
			t.Fatalf("Handle() returned error: %v", err)
		}
	}

	if len(sender.sent) != 1 {
		t.Errorf("three deliveries of one message produced %d replies, want 1", len(sender.sent))
	}
}

func TestHandleAnswersEachDistinctMessage(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	for _, messageID := range []string{"4127", "4128", "4129"} {
		if err := svc.Handle(t.Context(), incoming(messageID)); err != nil {
			t.Fatalf("Handle() returned error: %v", err)
		}
	}

	if len(sender.sent) != 3 {
		t.Errorf("sent %d replies, want 3", len(sender.sent))
	}
}

// TestFailedDeliveryCanBeRetried checks the ordering of deduplication against
// processing: marking a delivery handled before it succeeds would lose the
// message, because the retry would be discarded as a duplicate.
func TestFailedDeliveryCanBeRetried(t *testing.T) {
	sender := &fakeSender{err: errors.New("telegram unreachable")}
	svc, _ := newTestService(t, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err == nil {
		t.Fatal("Handle() succeeded despite the send failing")
	}

	// The provider retries, and this time the send works.
	sender.err = nil
	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("the retry returned error: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Errorf("the retry after a failed send produced %d replies, want 1", len(sender.sent))
	}
}

func TestHandleRecordsBothSidesOfTheExchange(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(t, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: "219847362",
	})
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}

	history, err := svc.History(t.Context(), conv.ID)
	if err != nil {
		t.Fatalf("History() returned error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("transcript holds %d messages, want 2", len(history))
	}
	if history[0].Direction != conversation.DirectionInbound {
		t.Errorf("first message direction = %q, want inbound", history[0].Direction)
	}
	if history[0].ExternalMessageID != "4127" {
		t.Errorf("inbound message lost its provider id: %q", history[0].ExternalMessageID)
	}
	if history[1].Direction != conversation.DirectionOutbound {
		t.Errorf("second message direction = %q, want outbound", history[1].Direction)
	}
	if history[1].Text != sender.sent[0].Text {
		t.Error("the recorded reply differs from the one that was sent")
	}
}

func TestMessagesFromOneCustomerShareAConversation(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(t, sender)

	for _, messageID := range []string{"4127", "4128"} {
		if err := svc.Handle(t.Context(), incoming(messageID)); err != nil {
			t.Fatalf("Handle() returned error: %v", err)
		}
	}

	conv, _ := store.FindOrOpen(t.Context(), conversation.Conversation{
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: "219847362",
	})
	history, _ := svc.History(t.Context(), conv.ID)

	if len(history) != 4 {
		t.Errorf("transcript holds %d messages, want 4 across two exchanges", len(history))
	}
}

// TestAssistantStaysQuietWhenAColleagueIsHandling encodes a product rule in
// code rather than in a prompt: no wording in a customer's message can talk the
// system into answering a conversation a person has taken over.
func TestAssistantStaysQuietWhenAColleagueIsHandling(t *testing.T) {
	for _, state := range []conversation.State{
		conversation.StateHumanRequested,
		conversation.StateHumanActive,
	} {
		t.Run(string(state), func(t *testing.T) {
			sender := &fakeSender{}
			svc, store := newTestService(t, sender)

			// The first message opens the conversation and is answered.
			if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
				t.Fatalf("Handle() returned error: %v", err)
			}

			conv, _ := store.FindOrOpen(t.Context(), conversation.Conversation{
				Provider:         messaging.ProviderTelegram,
				ExternalThreadID: "219847362",
			})
			if err := conv.TransitionTo(state, testNow); err != nil {
				t.Fatalf("TransitionTo() returned error: %v", err)
			}
			if err := store.Save(t.Context(), conv); err != nil {
				t.Fatalf("Save() returned error: %v", err)
			}

			if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
				t.Fatalf("Handle() returned error: %v", err)
			}

			if len(sender.sent) != 1 {
				t.Errorf("the assistant replied %d times, want 1 before the handover only", len(sender.sent))
			}

			// The message is still recorded, so the colleague sees it.
			history, _ := svc.History(t.Context(), conv.ID)
			if len(history) != 3 {
				t.Errorf("transcript holds %d messages, want the unanswered one kept", len(history))
			}
		})
	}
}

func TestHandleRejectsInvalidMessages(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	msg := incoming("4127")
	msg.ExternalThreadID = ""

	if err := svc.Handle(t.Context(), msg); !errors.Is(err, messaging.ErrInvalidEnvelope) {
		t.Errorf("error = %v, want ErrInvalidEnvelope", err)
	}
	if len(sender.sent) != 0 {
		t.Error("an invalid message produced a reply")
	}
}

func TestHandleFailsWhenTheChannelHasNoSender(t *testing.T) {
	svc, _ := newTestService(t, &fakeSender{})

	msg := incoming("4127")
	msg.Provider = messaging.ProviderViber

	if err := svc.Handle(t.Context(), msg); err == nil {
		t.Fatal("Handle() succeeded for a channel with no sender configured")
	}
}

func TestHandleSurfacesDeliveryFailures(t *testing.T) {
	sendErr := errors.New("telegram unreachable")
	svc, _ := newTestService(t, &fakeSender{err: sendErr})

	if err := svc.Handle(t.Context(), incoming("4127")); !errors.Is(err, sendErr) {
		t.Errorf("error = %v, want it to wrap the delivery failure", err)
	}
}

func TestHandleExplainsUnreadableContent(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	msg := incoming("4127")
	msg.Content = messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "voice message"}

	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if !strings.Contains(sender.sent[0].Text, "voice message") {
		t.Errorf("reply does not say what could not be read: %q", sender.sent[0].Text)
	}
}

// TestHandleDoesNotClaimToHaveBooked guards the rule that matters most in this
// product: the assistant must never tell a customer an appointment exists
// before the scheduling system has confirmed one.
func TestHandleDoesNotClaimToHaveBooked(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	reply := strings.ToLower(sender.sent[0].Text)
	for _, claim := range []string{"you are booked", "you're booked", "confirmed", "your appointment is"} {
		if strings.Contains(reply, claim) {
			t.Errorf("reply claims a booking that was never made: %q", sender.sent[0].Text)
		}
	}
}
