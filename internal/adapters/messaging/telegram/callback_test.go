package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const testStaffChat = "-1001234567890"

const testConversationID = "01a062bc-3adb-722a-b7a8-eaf79378f0f6"

type answeredQuery struct {
	id   string
	text string
}

type clearedKeyboard struct {
	chatID    string
	messageID int64
}

type fakeButtons struct {
	answered []answeredQuery
	cleared  []clearedKeyboard
}

func (b *fakeButtons) AnswerCallback(_ context.Context, id, text string) error {
	b.answered = append(b.answered, answeredQuery{id: id, text: text})
	return nil
}

func (b *fakeButtons) ClearKeyboard(_ context.Context, chatID string, messageID int64) error {
	b.cleared = append(b.cleared, clearedKeyboard{chatID: chatID, messageID: messageID})
	return nil
}

type staffCommandCall struct {
	command        assistant.StaffCommand
	conversationID string
}

type fakeDesk struct {
	commands []staffCommandCall
	replies  []assistant.StaffReply
	err      error
}

func (d *fakeDesk) RelayStaffReply(_ context.Context, reply assistant.StaffReply) error {
	d.replies = append(d.replies, reply)
	return d.err
}

func (d *fakeDesk) RunStaffCommand(
	_ context.Context,
	command assistant.StaffCommand,
	conversationID string,
) (string, error) {
	d.commands = append(d.commands, staffCommandCall{command: command, conversationID: conversationID})
	if d.err != nil {
		return "", d.err
	}
	return "The assistant is answering this customer again.", nil
}

type fakeThreads struct{}

func (fakeThreads) LinkStaffThread(context.Context, string, string) error { return nil }

func (fakeThreads) ConversationForStaffThread(context.Context, string) (string, error) {
	return testConversationID, nil
}

// staffHandler wires a handler the way the gateway does, minus the HTTP client
// the real staff desk needs. The fields are set directly because a test in this
// package may, and standing up a live Telegram client to observe a routing
// decision would be testing the client instead.
func staffHandler(messages MessageHandler, desk StaffDesk, buttons Buttons) (*Handler, *[]string) {
	h := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithStaffChat(testStaffChat),
		WithButtons(buttons),
	)
	h.desk = desk
	h.threads = fakeThreads{}

	var said []string
	h.staffReplies = func(_ context.Context, text string) error {
		said = append(said, text)
		return nil
	}
	return h, &said
}

// TestButtonPressBecomesTheCustomersAnswer is the whole point of the keyboard:
// tapping a time has to be indistinguishable, above this adapter, from typing
// it. Nothing further in is told a button was involved.
func TestButtonPressBecomesTheCustomersAnswer(t *testing.T) {
	messages := &recordingHandler{}
	buttons := &fakeButtons{}

	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithButtons(buttons))

	rec := post(t, handler, "s3cret-token", fixture(t, "button_press.json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 1 {
		t.Fatalf("handled %d messages, want the press to arrive as one", len(messages.got))
	}

	got := messages.got[0]
	if got.Content.Text != "16:00" {
		t.Errorf("text = %q, want the label of the button that was pressed", got.Content.Text)
	}
	if got.ExternalThreadID != "219847362" {
		t.Errorf("thread = %q, want the chat the keyboard was shown in", got.ExternalThreadID)
	}

	// The press is identified by the query, not by the message it sits under:
	// every press of one keyboard shares that message.
	if got.ExternalMessageID != "callback:4382042044176398" {
		t.Errorf("message id = %q, want the callback query's own id", got.ExternalMessageID)
	}

	if len(buttons.answered) != 1 || buttons.answered[0].id != "4382042044176398" {
		t.Errorf("answered = %+v, want the press acknowledged once", buttons.answered)
	}

	// The question has been answered, so it stops being answerable.
	if len(buttons.cleared) != 1 || buttons.cleared[0].messageID != 4128 {
		t.Errorf("cleared = %+v, want the keyboard taken away", buttons.cleared)
	}
}

// TestUnreadableButtonPressIsExplained covers a keyboard from a message this
// deployment did not send, or one Telegram no longer describes. Saying so beats
// a button that quietly does nothing.
func TestUnreadableButtonPressIsExplained(t *testing.T) {
	messages := &recordingHandler{}
	buttons := &fakeButtons{}

	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithButtons(buttons))

	// The stored fixture carries data no keyboard in it accounts for.
	rec := post(t, handler, "s3cret-token", fixture(t, "callback_query.json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Errorf("an unreadable press reached the application: %+v", messages.got)
	}
}

// TestButtonPressAsksForRedeliveryWhenProcessingFails: a press that could not
// be handled is worth having again, and the query id makes the repeat safe to
// deduplicate.
func TestButtonPressAsksForRedeliveryWhenProcessingFails(t *testing.T) {
	messages := &recordingHandler{err: errors.New("firestore unreachable")}

	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithButtons(&fakeButtons{}))

	rec := post(t, handler, "s3cret-token", fixture(t, "button_press.json"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d so Telegram sends it again",
			rec.Code, http.StatusInternalServerError)
	}
}

// TestStaffButtonHandsTheConversationBack is the fix for a colleague having to
// reply to exactly the right notification and then type an instruction into it.
// The conversation travels in the button, so the press works wherever it lands.
func TestStaffButtonHandsTheConversationBack(t *testing.T) {
	messages := &recordingHandler{}
	desk := &fakeDesk{}
	buttons := &fakeButtons{}

	handler, said := staffHandler(messages, desk, buttons)
	rec := post(t, handler, "s3cret-token", fixture(t, "staff_button_press.json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(messages.got) != 0 {
		t.Errorf("a staff button was handled as a customer message: %+v", messages.got)
	}

	if len(desk.commands) != 1 {
		t.Fatalf("ran %d commands, want 1", len(desk.commands))
	}
	if desk.commands[0].command != assistant.CommandResume {
		t.Errorf("command = %q, want %q", desk.commands[0].command, assistant.CommandResume)
	}
	if desk.commands[0].conversationID != testConversationID {
		t.Errorf("conversation = %q, want the one named on the button", desk.commands[0].conversationID)
	}

	if len(*said) != 1 {
		t.Errorf("the staff chat was told %d things, want 1", len(*said))
	}

	// The two staff buttons are a switch rather than a question: whoever takes
	// a conversation hands it back with the other one later.
	if len(buttons.cleared) != 0 {
		t.Errorf("the staff keyboard was taken away: %+v", buttons.cleared)
	}
}

// TestStaffButtonOutsideTheStaffChatIsRefused: the buttons carry a conversation
// and change who answers it, so a press counts only where the business is the
// one pressing.
func TestStaffButtonOutsideTheStaffChatIsRefused(t *testing.T) {
	messages := &recordingHandler{}
	desk := &fakeDesk{}

	handler := NewHandler(testWebhook(), messages,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithStaffChat("-100999999999"),
		WithButtons(&fakeButtons{}),
	)
	handler.desk = desk
	handler.threads = fakeThreads{}

	rec := post(t, handler, "s3cret-token", fixture(t, "staff_button_press.json"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(desk.commands) != 0 {
		t.Errorf("a staff command ran from outside the staff chat: %+v", desk.commands)
	}
}

func TestKeyboardWidthFollowsTheLongestLabel(t *testing.T) {
	tests := map[string]struct {
		labels []string
		want   int
	}{
		"times sit three abreast":   {labels: []string{"15:30", "16:00", "16:30"}, want: 3},
		"short sentences sit two":   {labels: []string{"Yes, book it", "Another time"}, want: 2},
		"a service name gets a row": {labels: []string{"Massage Motion Sport 115 minutes"}, want: 1},
		"the longest label decides": {labels: []string{"16:00", "Massage Motion Sport 115 min"}, want: 1},
		"armenian counts letters":   {labels: []string{"Այո", "Ոչ"}, want: 2},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			choices := make([]messaging.Choice, 0, len(tc.labels))
			for _, label := range tc.labels {
				choices = append(choices, messaging.Choice{Label: label})
			}

			markup := keyboardFor(choices)
			if markup == nil {
				t.Fatal("keyboardFor() returned no keyboard")
			}
			if got := len(markup.Keyboard[0]); got != tc.want {
				t.Errorf("first row holds %d buttons, want %d", got, tc.want)
			}
		})
	}
}

func TestKeyboardForNothingIsNoKeyboard(t *testing.T) {
	if markup := keyboardFor(nil); markup != nil {
		t.Errorf("keyboardFor(nil) = %+v, want no keyboard at all", markup)
	}
}

// TestStaffActionSurvivesTheRoundTrip guards the 64 bytes Telegram allows in
// callback data. An action and a conversation id have to fit inside it, and a
// truncated id would name a conversation that does not exist.
func TestStaffActionSurvivesTheRoundTrip(t *testing.T) {
	data := staffActionData(string(assistant.CommandResume), testConversationID)

	if len(data) > 64 {
		t.Fatalf("callback data is %d bytes, more than Telegram accepts", len(data))
	}

	action, ok := parseStaffAction(data)
	if !ok {
		t.Fatal("parseStaffAction() did not recognise data it had just built")
	}
	if action.Command != string(assistant.CommandResume) {
		t.Errorf("command = %q", action.Command)
	}
	if action.ConversationID != testConversationID {
		t.Errorf("conversation = %q, want %q", action.ConversationID, testConversationID)
	}
}

func TestParseStaffActionRejectsAnythingElse(t *testing.T) {
	for _, data := range []string{"", "c:1", "s:", "s:resume"} {
		if _, ok := parseStaffAction(data); ok {
			t.Errorf("parseStaffAction(%q) accepted a payload that names no conversation", data)
		}
	}
}
