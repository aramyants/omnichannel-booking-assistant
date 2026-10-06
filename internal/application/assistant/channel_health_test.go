package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type outageStaff struct {
	recordingStaff
	inbound []string
}

func (s *outageStaff) NotifyInbound(_ context.Context, _ HandoffNotice, text string) error {
	s.inbound = append(s.inbound, text)
	return nil
}

func TestRestrictedChannelSavesMessagesForStaffWithoutModelOrBookingActions(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{textResponse("must not run")}}
	calendar := defaultScheduling()
	staff := &outageStaff{}
	svc, store := newAIServiceWithStaff(t, model, calendar, sender, staff)
	svc.senders[messaging.ProviderWhatsApp] = sender
	svc.deliveryAvailable = func(_ context.Context, p messaging.Provider) (bool, error) {
		return p != messaging.ProviderWhatsApp, nil
	}
	msg := incoming("restricted-one")
	msg.Provider = messaging.ProviderWhatsApp
	for range 3 {
		if err := svc.Handle(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	msg.ExternalMessageID, msg.Content.Text = "restricted-two", "Please call me"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 || len(calendar.created) != 0 || len(sender.sent) != 0 || len(staff.notices) != 1 || len(staff.inbound) != 1 {
		t.Fatalf("model=%d bookings=%d replies=%d handoffs=%d inbound=%d", model.calls, len(calendar.created), len(sender.sent), len(staff.notices), len(staff.inbound))
	}
	notice := staff.notices[0]
	if notice.Reason != ReasonChannelUnavailable || !notice.Reason.Urgent() {
		t.Fatalf("notice=%+v", notice)
	}
	conv, err := store.FindByID(t.Context(), notice.ConversationID)
	if err != nil || conv.State != conversation.StateHumanRequested {
		t.Fatalf("conv=%+v err=%v", conv, err)
	}
	history, err := store.Recent(t.Context(), conv.ID, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%d err=%v", len(history), err)
	}
	// Other channels retain their ordinary conversation flow.
	if err := svc.Handle(t.Context(), incoming("telegram-working")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || len(sender.sent) != 1 {
		t.Fatal("WhatsApp outage stopped Telegram")
	}
}

func TestAvailabilityStorageFailureRetriesWithoutLosingInbound(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(t, sender)
	svc.deliveryAvailable = func(context.Context, messaging.Provider) (bool, error) { return false, errors.New("store unavailable") }
	msg := incoming("health-store-retry")
	if err := svc.Handle(t.Context(), msg); err == nil {
		t.Fatal("storage failure hidden")
	}
	svc.deliveryAvailable = func(context.Context, messaging.Provider) (bool, error) { return true, nil }
	if err := svc.Handle(t.Context(), msg); err != nil || len(sender.sent) != 1 {
		t.Fatalf("retry replies=%d error=%v", len(sender.sent), err)
	}
}
