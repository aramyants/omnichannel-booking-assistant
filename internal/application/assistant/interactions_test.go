package assistant

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type trackedSender struct {
	fakeSender
	retired []string
}

func (s *trackedSender) SendTracked(ctx context.Context, msg messaging.Outgoing) (string, error) {
	if err := s.Send(ctx, msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("sent-%d", len(s.sent)), nil
}
func (s *trackedSender) RetireChoices(_ context.Context, threadID, messageID string) error {
	s.retired = append(s.retired, threadID+":"+messageID)
	return nil
}

func TestTextAnswerRetiresKeyboardAcrossServiceInstances(t *testing.T) {
	sender := &trackedSender{}
	svc, store := newTestService(t, sender)
	if err := svc.Handle(t.Context(), incomingText("1", "/start")); err != nil {
		t.Fatal(err)
	}
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: messaging.ProviderTelegram, ExternalThreadID: incoming("1").ExternalThreadID})
	if err != nil || conv.LastChoiceMessageID != "sent-1" {
		t.Fatalf("keyboard was not persisted: %+v %v", conv, err)
	}
	// Simulate another Cloud Run instance, with no local knowledge of the keyboard.
	other := *svc
	if err := other.Handle(t.Context(), incomingText("2", "Hello")); err != nil {
		t.Fatal(err)
	}
	if len(sender.retired) != 1 || sender.retired[0] != conv.ExternalThreadID+":sent-1" {
		t.Fatalf("old keyboard not removed: %v", sender.retired)
	}
	updated, _ := store.FindByID(t.Context(), conv.ID)
	if updated.LastChoiceMessageID != "" {
		t.Fatal("old keyboard remains actionable")
	}
}

func TestConsumedCallbackCanRetryAfterReplyDeliveryFailure(t *testing.T) {
	sender := &trackedSender{}
	svc, _ := newTestService(t, sender)
	if err := svc.Handle(t.Context(), incomingText("1", "/start")); err != nil {
		t.Fatal(err)
	}
	msg := incomingText("2", "Book a visit")
	msg.ChoiceMessageID = "sent-1"
	sender.err = errors.New("send unavailable")
	if err := svc.Handle(t.Context(), msg); err == nil {
		t.Fatal("send failure was hidden")
	}
	sender.err = nil
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if got := sender.sent[1].Text; got == expiredChoice(languageArmenian) {
		t.Fatal("a retry of the same callback was discarded as expired")
	}
}

func TestOldButtonCannotConfirmANewerDraft(t *testing.T) {
	sender := &trackedSender{}
	calendar := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{prepareCall("prepare"), textResponse("Shall I book this?")}}
	svc, _ := newAIService(t, model, calendar, sender)
	if err := svc.Handle(t.Context(), incoming("1")); err != nil {
		t.Fatal(err)
	}
	old := incomingText("2", "Yes, book it")
	old.ChoiceMessageID = "old-confirmation"
	if err := svc.Handle(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || len(calendar.created) != 0 {
		t.Fatal("expired confirmation reached booking logic")
	}
	if len(sender.retired) != 0 {
		t.Fatal("expired button retired the current valid confirmation")
	}
	if len(sender.sent) != 2 {
		t.Fatal("expired choice received no explanation")
	}
}

type orderedModel struct {
	mu       sync.Mutex
	requests []ai.Request
	entered  chan struct{}
	release  chan struct{}
}

func (m *orderedModel) Model() string { return "ordered" }
func (m *orderedModel) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	first := len(m.requests) == 1
	m.mu.Unlock()
	if first {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return ai.Response{}, ctx.Err()
		}
	}
	return ai.Response{Text: "Received."}, nil
}

func TestDistinctMessagesSerializeAcrossInstances(t *testing.T) {
	model := &orderedModel{entered: make(chan struct{}), release: make(chan struct{})}
	svc, _ := newAIService(t, model, defaultScheduling(), &fakeSender{})
	other := *svc
	finished := make(chan error, 2)
	go func() { finished <- svc.Handle(t.Context(), incomingText("first", "Garik")) }()
	select {
	case <-model.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first message did not start")
	}
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		finished <- other.Handle(t.Context(), incomingText("second", "Grigoryan"))
	}()
	<-secondStarted
	// Holding the shared conversation lease must prevent a second model call.
	time.Sleep(150 * time.Millisecond)
	model.mu.Lock()
	count := len(model.requests)
	model.mu.Unlock()
	close(model.release)
	for range 2 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("%d overlapping model calls", count)
	}
	if got := model.requests[1].Messages; len(got) != 3 || got[0].Text != "Garik" || got[1].Role != ai.RoleAssistant || got[2].Text != "Grigoryan" {
		t.Fatalf("second request lost prior conversation: %+v", got)
	}
}
