package assistant

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestTerminalSendOutcomeKeepsTheDeliveryClaim(t *testing.T) {
	for _, outcome := range []error{messaging.ErrDeliveryUncertain, messaging.ErrDeliveryRejected} {
		t.Run(outcome.Error(), func(t *testing.T) {
			sender := &fakeSender{err: fmt.Errorf("provider: %w", outcome)}
			svc, _ := newTestService(t, sender)
			msg := incoming("terminal-send")
			if err := svc.Handle(t.Context(), msg); !errors.Is(err, outcome) {
				t.Fatalf("send outcome was lost: %v", err)
			}
			sender.err = nil
			if err := svc.Handle(t.Context(), msg); err != nil || len(sender.sent) != 0 {
				t.Fatalf("redelivery repeated a terminal send: %v %+v", err, sender.sent)
			}
			msg.ExternalMessageID = "fresh-turn"
			if err := svc.Handle(t.Context(), msg); err != nil || len(sender.sent) != 1 {
				t.Fatalf("a new customer turn must still work: %v %+v", err, sender.sent)
			}
		})
	}
}
