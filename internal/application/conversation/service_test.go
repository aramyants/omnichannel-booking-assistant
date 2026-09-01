package conversation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

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

func newTestService(sender Sender) *Service {
	return NewService(
		map[messaging.Provider]Sender{messaging.ProviderTelegram: sender},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func incoming() messaging.Envelope {
	return messaging.Envelope{
		Provider:          messaging.ProviderTelegram,
		ExternalMessageID: "4127",
		ExternalUserID:    "219847362",
		ExternalThreadID:  "219847362",
		SentAt:            time.Unix(1756728000, 0).UTC(),
		ReceivedAt:        time.Unix(1756728001, 0).UTC(),
		Sender:            messaging.Sender{DisplayName: "Anna", Language: "hy"},
		Content:           messaging.Content{Type: messaging.ContentTypeText, Text: "Can I book Friday?"},
	}
}

func TestHandleRepliesIntoTheSameConversation(t *testing.T) {
	sender := &fakeSender{}

	if err := newTestService(sender).Handle(t.Context(), incoming()); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(sender.sent))
	}
	reply := sender.sent[0]
	if reply.Provider != messaging.ProviderTelegram {
		t.Errorf("provider = %q, want telegram", reply.Provider)
	}
	if reply.ExternalThreadID != "219847362" {
		t.Errorf("thread = %q, want 219847362", reply.ExternalThreadID)
	}
	if !strings.Contains(reply.Text, "Anna") {
		t.Errorf("reply does not address the customer by name: %q", reply.Text)
	}
}

// TestHandleDoesNotClaimToHaveBooked guards the rule that matters most in this
// product: the assistant must never tell a customer an appointment exists
// before the scheduling system has confirmed one.
func TestHandleDoesNotClaimToHaveBooked(t *testing.T) {
	sender := &fakeSender{}

	if err := newTestService(sender).Handle(t.Context(), incoming()); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	reply := strings.ToLower(sender.sent[0].Text)
	for _, claim := range []string{"you are booked", "you're booked", "confirmed", "your appointment is"} {
		if strings.Contains(reply, claim) {
			t.Errorf("reply claims a booking that was never made: %q", sender.sent[0].Text)
		}
	}
}

func TestHandleExplainsUnreadableContent(t *testing.T) {
	sender := &fakeSender{}

	msg := incoming()
	msg.Content = messaging.Content{Type: messaging.ContentTypeUnsupported, Description: "voice message"}

	if err := newTestService(sender).Handle(t.Context(), msg); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if !strings.Contains(sender.sent[0].Text, "voice message") {
		t.Errorf("reply does not say what could not be read: %q", sender.sent[0].Text)
	}
}

func TestHandleRejectsInvalidMessages(t *testing.T) {
	sender := &fakeSender{}

	msg := incoming()
	msg.ExternalThreadID = ""

	err := newTestService(sender).Handle(t.Context(), msg)
	if !errors.Is(err, messaging.ErrInvalidEnvelope) {
		t.Errorf("error = %v, want ErrInvalidEnvelope", err)
	}
	if len(sender.sent) != 0 {
		t.Error("an invalid message produced a reply")
	}
}

func TestHandleFailsWhenTheChannelHasNoSender(t *testing.T) {
	msg := incoming()
	msg.Provider = messaging.ProviderViber

	if err := newTestService(&fakeSender{}).Handle(t.Context(), msg); err == nil {
		t.Fatal("Handle() succeeded for a channel with no sender configured")
	}
}

// TestHandleSurfacesDeliveryFailures matters because the caller uses the error
// to decide whether the provider should redeliver the message.
func TestHandleSurfacesDeliveryFailures(t *testing.T) {
	sendErr := errors.New("telegram unreachable")

	err := newTestService(&fakeSender{err: sendErr}).Handle(t.Context(), incoming())
	if !errors.Is(err, sendErr) {
		t.Errorf("error = %v, want it to wrap the delivery failure", err)
	}
}
