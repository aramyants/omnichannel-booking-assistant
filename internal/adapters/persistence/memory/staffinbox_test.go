package memory

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/staffinbox"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestInboxListPaginationUsesStableBoundary(t *testing.T) {
	store := New()
	at := time.Now()
	for n := 1; n <= 11; n++ {
		candidate := conv(fmt.Sprintf("conv-%02d", n), fmt.Sprintf("%02d", n))
		candidate.LastMessageAt = at
		if _, err := store.FindOrOpen(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	foreign := conv("foreign", "wa")
	foreign.Provider = messaging.ProviderWhatsApp
	foreign.LastMessageAt = at.Add(time.Hour)
	_, _ = store.FindOrOpen(t.Context(), foreign)
	first, err := store.TelegramConversations(t.Context(), nil, 8)
	if err != nil || len(first.Items) != 8 || first.Next == nil {
		t.Fatal("first page unavailable")
	}
	boundary := first.Items[7]
	boundary.LastMessageAt = at.Add(time.Hour)
	if err := store.Save(t.Context(), boundary); err != nil {
		t.Fatal(err)
	}
	second, err := store.TelegramConversations(t.Context(), first.Next, 8)
	if err != nil || len(second.Items) != 3 || second.Next != nil {
		t.Fatalf("second page = %d, %v", len(second.Items), err)
	}
	seen := map[string]bool{}
	for _, page := range [][]conversation.Conversation{first.Items, second.Items} {
		for _, item := range page {
			if seen[item.ID] || item.Provider != messaging.ProviderTelegram {
				t.Fatal("duplicate or wrong provider")
			}
			seen[item.ID] = true
		}
	}
}

func TestInboxPageExpires(t *testing.T) {
	at := time.Now()
	store := New(WithClock(func() time.Time { return at }))
	if err := store.SaveInboxPage(t.Context(), staffinbox.Page{ID: "page", ChatID: "staff", ExpiresAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InboxPage(t.Context(), "page"); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	if _, err := store.InboxPage(t.Context(), "page"); !errors.Is(err, staffinbox.ErrPageExpired) {
		t.Fatal("expired page remained usable")
	}
}
