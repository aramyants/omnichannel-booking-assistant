package telegram

import (
	"context"
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type contactRepository struct {
	mu   sync.Mutex
	rows map[string]notifications.Entry
}

func (r *contactRepository) TransactNotifications(_ context.Context, keys []string, fn func(map[string]*notifications.Entry) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := map[string]*notifications.Entry{}
	for _, k := range keys {
		v := r.rows[k]
		rows[k] = &v
	}
	if err := fn(rows); err != nil {
		return err
	}
	for _, k := range keys {
		r.rows[k] = *rows[k]
	}
	return nil
}
func (r *contactRepository) GetNotification(_ context.Context, key string) (notifications.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows[key], nil
}
func (r *contactRepository) PendingNotifications(context.Context) ([]notifications.Entry, error) {
	return nil, nil
}
func TestTelegramContactCannotLinkSomeoneElsesPhoneOrGroup(t *testing.T) {
	for _, tc := range []struct {
		name, chatType            string
		chatID, userID, contactID int
		want                      bool
	}{{"own private", "private", 123, 123, 123, true}, {"someone else's contact", "private", 123, 123, 999, false}, {"group", "group", 456, 123, 123, false}, {"mismatched private chat", "private", 456, 123, 123, false}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &contactRepository{rows: map[string]notifications.Entry{}}
			service := &notifications.Service{Repo: repo}
			if err := service.RequestContact(t.Context(), "123", "hy"); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			}))
			defer server.Close()
			handler := NativeNotifications{Service: service, Client: NewClient("test-token", WithBaseURL(server.URL)), StaffChatID: "staff"}
			body, _ := json.Marshal(map[string]any{"update_id": 1, "message": map[string]any{"message_id": 1, "date": time.Now().Unix(), "chat": map[string]any{"id": tc.chatID, "type": tc.chatType}, "from": map[string]any{"id": tc.userID, "language_code": "hy"}, "contact": map[string]any{"user_id": tc.contactID, "phone_number": "+37491123456"}}})
			_, err := handler.HandleTelegram(t.Context(), body)
			if err != nil {
				t.Fatal(err)
			}
			row, err := repo.GetNotification(t.Context(), notifications.Key("telegram_phone", "+37491123456"))
			if err != nil {
				t.Fatal(err)
			}
			if (row.Contact.Chat == "123") != tc.want {
				t.Fatal("phone ownership check failed")
			}
		})
	}
}

func TestOnlyStaffCanRecordANativeNotificationRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		chat int
		want bool
	}{{"client", 123, false}, {"staff", 999, true}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &contactRepository{rows: map[string]notifications.Entry{}}
			service := &notifications.Service{Repo: repo}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			}))
			defer server.Close()
			handler := NativeNotifications{Service: service, Client: NewClient("fixture", WithBaseURL(server.URL)), StaffChatID: "999"}
			body, _ := json.Marshal(map[string]any{"message": map[string]any{"chat": map[string]any{"id": tc.chat, "type": "private"}, "from": map[string]any{"id": tc.chat}, "text": "/notification_allow +37491123456 hy"}})
			_, err := handler.HandleTelegram(t.Context(), body)
			if err != nil {
				t.Fatal(err)
			}
			row, _ := repo.GetNotification(t.Context(), notifications.Key("booking_phone", "+37491123456"))
			if row.Contact.ExpiresAt.After(time.Now()) != tc.want {
				t.Fatal("staff permission boundary failed")
			}
		})
	}
}
