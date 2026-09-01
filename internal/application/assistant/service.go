// Package conversation turns an incoming customer message into a reply.
//
// It is the seam the AI orchestrator and the booking workflow will be built
// behind. For now it answers deterministically, which is enough to prove the
// path from a provider webhook back out to the customer.
package conversation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Sender delivers a message on the channel it names. Each provider adapter
// implements it for its own channel.
type Sender interface {
	Send(ctx context.Context, msg messaging.Outgoing) error
}

// Service handles one inbound message at a time.
type Service struct {
	senders map[messaging.Provider]Sender
	logger  *slog.Logger
}

// NewService returns a Service that delivers replies through senders, keyed by
// the channel each one serves.
func NewService(senders map[messaging.Provider]Sender, logger *slog.Logger) *Service {
	return &Service{senders: senders, logger: logger}
}

// Handle processes one normalised message and answers it.
func (s *Service) Handle(ctx context.Context, msg messaging.Envelope) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	sender, ok := s.senders[msg.Provider]
	if !ok {
		return fmt.Errorf("no sender configured for %s", msg.Provider)
	}

	s.logger.InfoContext(ctx, "handling a customer message",
		"provider", string(msg.Provider),
		"dedupe_key", msg.DedupeKey(),
		"content_type", string(msg.Content.Type),
		// The message body is not logged. It is customer content, and the
		// system has no reason to keep a second copy of it in the logs.
		"content_length", len(msg.Content.Text),
	)

	return sender.Send(ctx, msg.Reply(s.compose(msg)))
}

// compose builds the reply text.
//
// This is where the AI orchestrator will take over. Until then the replies are
// deliberately honest about what the assistant can and cannot yet do, rather
// than pretending to take a booking it has no way to make.
func (s *Service) compose(msg messaging.Envelope) string {
	if msg.Content.Type == messaging.ContentTypeUnsupported {
		return fmt.Sprintf(
			"Thanks%s. I can only read text messages at the moment, so I could not open your %s. Could you describe what you need in a message?",
			greetingName(msg.Sender.DisplayName), msg.Content.Description,
		)
	}

	return fmt.Sprintf(
		"Thanks%s, I have your message. Booking is not connected yet, so a colleague will follow up with you shortly.",
		greetingName(msg.Sender.DisplayName),
	)
}

func greetingName(name string) string {
	if name == "" {
		return ""
	}
	return " " + name
}
