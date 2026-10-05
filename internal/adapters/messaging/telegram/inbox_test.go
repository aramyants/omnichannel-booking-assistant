package telegram

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func inboxFixture(t *testing.T) (*Handler, *memory.Store, *fakeDesk, func() []sendMessageRequest) {
	t.Helper()
	store := memory.New()
	var mu sync.Mutex
	var sent []sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message sendMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&message)
		mu.Lock()
		sent = append(sent, message)
		count := len(sent)
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, count)
	}))
	t.Cleanup(server.Close)
	client := NewClient(testToken, WithBaseURL(server.URL))
	desk := &fakeDesk{}
	h := NewHandler(testWebhook(), &recordingHandler{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithStaffChat(testStaffChat), WithStaffDesk(desk, store, client, testStaffChat),
		WithStaffInbox(store, client, testStaffChat, time.FixedZone("Asia/Yerevan", 4*60*60)))
	return h, store, desk, func() []sendMessageRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]sendMessageRequest(nil), sent...)
	}
}

func seedInboxConversation(t *testing.T, store *memory.Store, provider messaging.Provider, thread string) conversation.Conversation {
	t.Helper()
	cust, err := store.FindOrCreateByChannelIdentity(t.Context(), customer.ChannelIdentity{
		CustomerID: "cust-" + thread, Provider: provider, ExternalUserID: thread,
	}, customer.Customer{ID: "cust-" + thread, Name: "Անի"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{ID: testConversationID,
		CustomerID: cust.ID, Provider: provider, ExternalThreadID: thread,
		State: conversation.StateAssistantActive, LastMessageAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestInboxRejectsPrivateHistoryOutsideStaffChat(t *testing.T) {
	h, store, desk, sent := inboxFixture(t)
	conv := seedInboxConversation(t, store, messaging.ProviderTelegram, "123")
	for _, action := range []string{"open", "take", "resume", "next", "older", "back", "list"} {
		h.handleCallback(t.Context(), Callback{ChatID: "123", Data: inboxData(action, conv.ID)})
	}
	if len(sent()) != 0 || len(desk.commands) != 0 {
		t.Fatal("customer chat accessed the staff inbox")
	}
}

func TestInboxHistoryCanReachTheWholeTranscript(t *testing.T) {
	h, store, _, sent := inboxFixture(t)
	conv := seedInboxConversation(t, store, messaging.ProviderTelegram, "123")
	for n := 1; n <= 17; n++ {
		if err := store.Append(t.Context(), conversation.Message{ID: fmt.Sprintf("00000000-0000-7000-8000-%012d", n),
			ConversationID: conv.ID, Direction: conversation.DirectionInbound, Text: fmt.Sprintf("sentence-%02d", n), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.inbox.history(t.Context(), conv.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		messages := sent()
		last := messages[len(messages)-1]
		for n := 1; n <= 17; n++ {
			if strings.Contains(last.Text, fmt.Sprintf("sentence-%02d", n)) {
				seen[fmt.Sprintf("%d", n)] = true
			}
		}
		conversationID, err := store.ConversationForStaffThread(t.Context(), fmt.Sprint(len(messages)))
		if err != nil || conversationID != conv.ID {
			t.Fatal("history reply not linked to customer")
		}
		next := ""
		for _, row := range last.ReplyMarkup.Keyboard {
			for _, button := range row {
				if len(button.CallbackData) > 64 {
					t.Fatal("callback exceeds Telegram limit")
				}
				if strings.HasPrefix(button.CallbackData, "inbox:older:") {
					next = button.CallbackData
				}
			}
		}
		if next == "" {
			break
		}
		if !h.handleCallback(t.Context(), Callback{ChatID: testStaffChat, Data: next}) {
			t.Fatal("callback rejected")
		}
	}
	if len(seen) != 17 {
		t.Fatalf("read %d of 17 messages", len(seen))
	}
}

func TestInboxNeverRelaysAnotherChannelThroughTelegram(t *testing.T) {
	h, store, desk, sent := inboxFixture(t)
	conv := seedInboxConversation(t, store, messaging.ProviderWhatsApp, "123")
	h.handleCallback(t.Context(), Callback{ChatID: testStaffChat, Data: inboxData("take", conv.ID)})
	if len(desk.commands) != 0 {
		t.Fatal("took over another provider")
	}
	for _, msg := range sent() {
		if strings.Contains(msg.Text, "Անի") {
			t.Fatal("another provider history was shown")
		}
	}
}

func TestInboxSplitsArmenianAndEmojiWithoutLosingText(t *testing.T) {
	text := strings.Repeat("Բարև Ձեզ 🌿\n", 1800)
	chunks := splitInboxText(text, 3500)
	if strings.Join(chunks, "") != text {
		t.Fatal("history text was lost")
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) || len(utf16.Encode([]rune(chunk))) > 3500 {
			t.Fatal("invalid or oversized Telegram text")
		}
	}
}

func TestStaffBotsCannotRelayMessages(t *testing.T) {
	body := []byte(`{"update_id":1,"message":{"message_id":5,"from":{"id":42,"is_bot":true},"chat":{"id":-1001234567890},"text":"hello","reply_to_message":{"message_id":1}}}`)
	if _, ok := ParseStaffMessage(body); ok {
		t.Fatal("another bot was accepted as staff")
	}
}
