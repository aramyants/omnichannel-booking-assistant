package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const (
	oldGroupID = "-5392776711"
	supergroup = "-1001234567890"
)

const upgradedGroupResponse = `{"ok":false,"error_code":400,` +
	`"description":"Bad Request: group chat was upgraded to a supergroup chat",` +
	`"parameters":{"migrate_to_chat_id":-1001234567890}}`

// upgradedGroupServer answers like Telegram after a group became a supergroup:
// the old id is refused and names the new one, which works.
func upgradedGroupServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		chats []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
			Scope  *struct {
				ChatID string `json:"chat_id"`
			} `json:"scope"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		chat := body.ChatID
		if chat == "" && body.Scope != nil {
			chat = body.Scope.ChatID
		}
		mu.Lock()
		chats = append(chats, chat)
		mu.Unlock()

		if chat == oldGroupID {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(upgradedGroupResponse))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"id":-1001234567890}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), chats...)
	}
}

func TestUpgradedGroupErrorsNameTheNewChat(t *testing.T) {
	srv, _ := upgradedGroupServer(t)
	client := NewClient(testToken, WithBaseURL(srv.URL))

	err := client.Send(t.Context(), messaging.Outgoing{
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: oldGroupID,
		Text:             "hello",
	})
	if err == nil {
		t.Fatal("Send() to an upgraded group succeeded")
	}
	if got := MigratedChatID(err); got != supergroup {
		t.Errorf("MigratedChatID() = %q, want %q", got, supergroup)
	}
	if !strings.Contains(err.Error(), supergroup) {
		t.Errorf("error %q does not say where the chat went", err)
	}
}

// TestResolveChatIDFollowsAnUpgradedGroup: the probe must be a call Telegram
// refuses for an upgraded group. getChat still answers for the old id, which is
// how a first version of this check missed a real migration.
func TestResolveChatIDFollowsAnUpgradedGroup(t *testing.T) {
	srv, _ := upgradedGroupServer(t)
	client := NewClient(testToken, WithBaseURL(srv.URL))

	got, err := client.ResolveChatID(t.Context(), oldGroupID)
	if err != nil {
		t.Fatalf("ResolveChatID() returned error: %v", err)
	}
	if got != supergroup {
		t.Errorf("ResolveChatID() = %q, want %q", got, supergroup)
	}

	unchanged, err := client.ResolveChatID(t.Context(), supergroup)
	if err != nil || unchanged != supergroup {
		t.Errorf("ResolveChatID(current) = %q, %v; want it unchanged", unchanged, err)
	}
}

// TestStaffNoticesFollowAGroupUpgradedWhileRunning: the notice that a customer
// is waiting is the urgent part, so it is not lost to a stale chat id.
func TestStaffNoticesFollowAGroupUpgradedWhileRunning(t *testing.T) {
	srv, chats := upgradedGroupServer(t)
	notifier, err := NewStaffNotifier(NewClient(testToken, WithBaseURL(srv.URL)), oldGroupID, nil)
	if err != nil {
		t.Fatalf("NewStaffNotifier() returned error: %v", err)
	}
	notice := assistant.HandoffNotice{
		ConversationID: "conv-1",
		Reason:         assistant.ReasonCustomerAsked,
		Provider:       messaging.ProviderMessenger,
	}

	if err := notifier.NotifyHandoff(t.Context(), notice); err != nil {
		t.Fatalf("first NotifyHandoff() returned error: %v", err)
	}
	if err := notifier.NotifyHandoff(t.Context(), notice); err != nil {
		t.Fatalf("second NotifyHandoff() returned error: %v", err)
	}

	want := []string{oldGroupID, supergroup, supergroup}
	got := chats()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chats = %v, want %v", got, want)
	}
}

func TestHandoffLinksOpenTheCustomersOwnChannel(t *testing.T) {
	cases := []struct {
		provider messaging.Provider
		userID   string
		want     string
	}{
		{messaging.ProviderMessenger, "7461203958812", messengerInboxURL},
		{messaging.ProviderInstagram, "17841400000000", instagramInboxURL},
		{messaging.ProviderWhatsApp, "37494768067", "https://wa.me/37494768067"},
		{messaging.ProviderTelegram, "219847362", "tg://user?id=219847362"},
	}
	for _, tc := range cases {
		text := formatHandoff(assistant.HandoffNotice{
			ConversationID: "conv-1",
			Reason:         assistant.ReasonCustomerAsked,
			Provider:       tc.provider,
			ExternalUserID: tc.userID,
		})
		if !strings.Contains(text, tc.want) {
			t.Errorf("%s notice = %q, want it to contain %q", tc.provider, text, tc.want)
		}
		if tc.provider != messaging.ProviderTelegram && strings.Contains(text, "tg://") {
			t.Errorf("%s notice links to Telegram: %q", tc.provider, text)
		}
	}
}
