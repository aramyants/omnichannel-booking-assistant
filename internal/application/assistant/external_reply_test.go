package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func staffAppReply(id string) messaging.ExternalReply {
	return messaging.ExternalReply{Provider: messaging.ProviderWhatsApp, ExternalMessageID: id,
		ExternalThreadID: "15550000002", SentAt: testNow, ReceivedAt: testNow,
		Content: messaging.Content{Type: messaging.ContentTypeText, Text: "I am checking your appointment personally."}}
}

func customerWhatsApp(id string) messaging.Envelope {
	msg := incomingText(id, "Can you help with my appointment?")
	msg.Provider = messaging.ProviderWhatsApp
	msg.ExternalThreadID, msg.ExternalUserID = "15550000002", "15550000002"
	return msg
}

func TestExternalReplyRecordsHumanContextAndStaysSilentUntilResume(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(t, sender)
	svc.senders[messaging.ProviderWhatsApp] = sender
	if err := svc.RecordExternalReply(t.Context(), staffAppReply("staff-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(t.Context(), customerWhatsApp("customer-1")); err != nil {
		t.Fatal(err)
	}
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "15550000002"})
	if err != nil || conv.State != conversation.StateHumanActive || len(sender.sent) != 0 {
		t.Fatalf("staff was interrupted: %+v %v", conv, err)
	}
	history, err := svc.History(t.Context(), conv.ID)
	if err != nil || len(history) != 2 || history[0].Direction != conversation.DirectionOutbound || history[0].Text != staffAppReply("staff-1").Content.Text {
		t.Fatalf("missing staff transcript: %+v %v", history, err)
	}
	svc.now = func() time.Time { return testNow.Add(time.Minute) }
	if _, err := svc.RunStaffCommand(t.Context(), CommandResume, conv.ID); err != nil {
		t.Fatal(err)
	}
	// Both a replay and a previously undelivered older echo must preserve resume.
	for _, id := range []string{"staff-1", "staff-delayed"} {
		if err := svc.RecordExternalReply(t.Context(), staffAppReply(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Handle(t.Context(), customerWhatsApp("customer-2")); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("resume was undone by late staff activity: %v", sender.sent)
	}
	history, _ = svc.History(t.Context(), conv.ID)
	count := 0
	for _, msg := range history {
		if msg.ExternalMessageID == "staff-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("echo replay duplicated transcript %d times", count)
	}
}

type pausedEchoModel struct {
	entered  chan struct{}
	release  chan struct{}
	response ai.Response
}

func (m *pausedEchoModel) Model() string { return "paused" }
func (m *pausedEchoModel) Complete(ctx context.Context, _ ai.Request) (ai.Response, error) {
	close(m.entered)
	select {
	case <-m.release:
		return m.response, nil
	case <-ctx.Done():
		return ai.Response{}, ctx.Err()
	}
}

func TestExternalReplyInterruptsOtherInstanceBeforeReplyOrTool(t *testing.T) {
	for _, response := range []ai.Response{textResponse("A stale answer."), prepareCall("prepare")} {
		t.Run(response.Text, func(t *testing.T) {
			model := &pausedEchoModel{entered: make(chan struct{}), release: make(chan struct{}), response: response}
			sender := &fakeSender{}
			calendar := defaultScheduling()
			svc, store := newAIService(t, model, calendar, sender)
			svc.senders[messaging.ProviderWhatsApp] = sender
			other := *svc
			finished := make(chan error, 1)
			go func() { finished <- svc.Handle(t.Context(), customerWhatsApp("customer-1")) }()
			select {
			case <-model.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("model did not start")
			}
			echoErr := other.RecordExternalReply(t.Context(), staffAppReply("staff-1"))
			close(model.release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if echoErr != nil {
				t.Fatal(echoErr)
			}
			if len(sender.sent) != 0 || len(calendar.checked) != 0 || len(calendar.created) != 0 {
				t.Fatal("stale turn sent a reply or executed a booking tool")
			}
			conv, _ := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: messaging.ProviderWhatsApp, ExternalThreadID: "15550000002"})
			if conv.State != conversation.StateHumanActive {
				t.Fatalf("stale turn overwrote human takeover: %+v", conv)
			}
			if err := svc.Handle(t.Context(), customerWhatsApp("customer-1")); err != nil {
				t.Fatal(err)
			}
			history, _ := svc.History(t.Context(), conv.ID)
			if len(history) != 2 {
				t.Fatalf("suppressed turn was processed again: %+v", history)
			}
		})
	}
}
