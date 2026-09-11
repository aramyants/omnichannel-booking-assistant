package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type messageHandlerFunc func(context.Context, messaging.Envelope) error

func (f messageHandlerFunc) Handle(ctx context.Context, msg messaging.Envelope) error {
	return f(ctx, msg)
}

type selectedMessage struct {
	chatID    string
	messageID int64
	text      string
}

type feedbackButtons struct {
	fakeButtons
	typing    func(context.Context, string) error
	selected  []selectedMessage
	selectErr error
}

func (b *feedbackButtons) SendTyping(ctx context.Context, chatID string) error {
	if b.typing != nil {
		return b.typing(ctx, chatID)
	}
	return nil
}

func (b *feedbackButtons) ShowSelection(_ context.Context, chatID string, messageID int64, text string) error {
	b.selected = append(b.selected, selectedMessage{chatID: chatID, messageID: messageID, text: text})
	return b.selectErr
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestButtonSelectionStaysVisibleBeforeProcessing(t *testing.T) {
	buttons := &feedbackButtons{}
	messages := messageHandlerFunc(func(_ context.Context, msg messaging.Envelope) error {
		if len(buttons.answered) != 1 {
			t.Error("processing began before the button was acknowledged")
		}
		if len(buttons.selected) != 1 || !strings.HasSuffix(buttons.selected[0].text, "\n\n✓ 16:00") {
			t.Errorf("selection is missing from the answered question: %+v", buttons.selected)
		}
		if msg.Content.Text != "16:00" {
			t.Errorf("display receipt changed the customer's answer: %q", msg.Content.Text)
		}
		if msg.ChoiceMessageID != "4128" {
			t.Errorf("lost the originating keyboard ID: %q", msg.ChoiceMessageID)
		}
		return nil
	})
	h := NewHandler(testWebhook(), messages, quietLogger(), WithButtons(buttons))
	rec := post(t, h, "s3cret-token", fixture(t, "button_press.json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := buttons.selected[0]; got.chatID != "219847362" || got.messageID != 4128 {
		t.Errorf("edited the wrong message: %+v", got)
	}
	if len(buttons.cleared) != 0 {
		t.Error("selection edit already removes the keyboard; a second edit is unnecessary")
	}
}

func TestSelectionEditFailureStillProcessesAnswerAndRetiresKeyboard(t *testing.T) {
	buttons := &feedbackButtons{selectErr: errors.New("message cannot be edited")}
	messages := &recordingHandler{}
	h := NewHandler(testWebhook(), messages, quietLogger(), WithButtons(buttons))
	rec := post(t, h, "s3cret-token", fixture(t, "button_press.json"))
	if rec.Code != http.StatusOK || len(messages.got) != 1 || len(buttons.cleared) != 1 {
		t.Fatalf("failed selection edit lost the answer: status=%d, messages=%d, retired=%d",
			rec.Code, len(messages.got), len(buttons.cleared))
	}
}

func TestTypingRefreshesUntilProcessingStops(t *testing.T) {
	var calls atomic.Int32
	typed := make(chan string, 20)
	buttons := &feedbackButtons{typing: func(ctx context.Context, chatID string) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > interactionTimeout {
			t.Error("typing call lacks the short feedback deadline")
		}
		calls.Add(1)
		typed <- chatID
		return nil
	}}
	h := NewHandler(testWebhook(), &recordingHandler{}, quietLogger(), WithButtons(buttons))
	stop := h.beginTypingEvery(t.Context(), "219847362", 10*time.Millisecond)
	defer stop()
	for i := 0; i < 2; i++ {
		select {
		case chatID := <-typed:
			if chatID != "219847362" {
				t.Errorf("typing in chat %q", chatID)
			}
		case <-time.After(time.Second):
			t.Fatal("typing status did not start and refresh")
		}
	}
	stop()
	before := calls.Load()
	time.Sleep(25 * time.Millisecond)
	if calls.Load() != before {
		t.Error("typing continued after the response finished")
	}
}

func TestSlowTypingCallDoesNotDelayMessageAndIsCancelledOnCompletion(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	buttons := &feedbackButtons{typing: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}}
	messages := messageHandlerFunc(func(_ context.Context, _ messaging.Envelope) error {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Error("typing did not begin while processing")
		}
		return nil
	})
	h := NewHandler(testWebhook(), messages, quietLogger(), WithButtons(buttons))
	rec := post(t, h, "s3cret-token", fixture(t, "text_message.json"))
	if rec.Code != http.StatusOK {
		t.Errorf("typing failure affected webhook status: %d", rec.Code)
	}
	select {
	case <-stopped:
	default:
		t.Error("request returned with an orphan typing call")
	}
}

func TestStaffChatterDoesNotShowCustomerTyping(t *testing.T) {
	var calls atomic.Int32
	buttons := &feedbackButtons{typing: func(context.Context, string) error {
		calls.Add(1)
		return nil
	}}
	h := NewHandler(testWebhook(), &recordingHandler{}, quietLogger(),
		WithButtons(buttons), WithStaffChat(testStaffChat))
	post(t, h, "s3cret-token", fixture(t, "group_message.json"))
	if calls.Load() != 0 {
		t.Error("typing was shown for staff conversation")
	}
}

type deadlineButtons struct {
	ackDeadline   time.Time
	clearDeadline time.Time
}

func (b *deadlineButtons) AnswerCallback(ctx context.Context, _, _ string) error {
	b.ackDeadline, _ = ctx.Deadline()
	return errors.New("telegram unavailable")
}

func (b *deadlineButtons) ClearKeyboard(ctx context.Context, _ string, _ int64) error {
	b.clearDeadline, _ = ctx.Deadline()
	return errors.New("telegram unavailable")
}

func TestCallbackFeedbackHasShortDeadlinesAndCannotFailDelivery(t *testing.T) {
	buttons := &deadlineButtons{}
	messages := &recordingHandler{}
	h := NewHandler(testWebhook(), messages, quietLogger(), WithButtons(buttons))
	rec := post(t, h, "s3cret-token", fixture(t, "button_press.json"))
	if rec.Code != http.StatusOK || len(messages.got) != 1 {
		t.Fatal("cosmetic API failures prevented processing")
	}
	for name, deadline := range map[string]time.Time{"ack": buttons.ackDeadline, "clear": buttons.clearDeadline} {
		if deadline.IsZero() || time.Until(deadline) > interactionTimeout {
			t.Errorf("%s lacks a short deadline: %v", name, deadline)
		}
	}
}

func TestSelectionReceiptPreservesUnicodeAndMessageLimits(t *testing.T) {
	question, answer := "Какой день вам удобнее?", "17 сентября"
	if got := selectedQuestion(question, answer); got != question+"\n\n✓ "+answer {
		t.Errorf("receipt = %q", got)
	}
	if once := selectedQuestion(question, answer); selectedQuestion(once, answer) != once {
		t.Error("a repeated callback duplicated the selection receipt")
	}
	if got := selectedQuestion(strings.Repeat("я", maxMessageRunes), answer); got != "" {
		t.Error("a selection receipt would exceed Telegram's text limit")
	}
}
