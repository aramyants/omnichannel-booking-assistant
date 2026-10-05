package telegramaccount

import (
	"context"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/gotd/td/tg"
)

type repo struct{ row notifications.Entry }

func (r *repo) GetNotification(context.Context, string) (notifications.Entry, error) {
	return r.row, nil
}
func (r *repo) PendingNotifications(context.Context) ([]notifications.Entry, error) { return nil, nil }
func (r *repo) TransactNotifications(_ context.Context, _ []string, change func(map[string]*notifications.Entry) error) error {
	row := r.row
	err := change(map[string]*notifications.Entry{storageKey: &row})
	if err == nil {
		r.row = row
	}
	return err
}

func TestSessionEncryptionBindsStudioAndFencesExpiredWorkers(t *testing.T) {
	r := new(repo)
	cfg := Config{AppID: 123, AppHash: "fixture", Phone: "+37494768067", Username: "emotion_concept", EncryptionKey: make([]byte, 32)}
	c, err := New(cfg, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.InstallSession(t.Context(), []byte("private authorization session")); err != nil {
		t.Fatal(err)
	}
	if string(r.row.SessionCiphertext) == "private authorization session" {
		t.Fatal("session persisted in plaintext")
	}
	r.row.LeaseOwner = "current"
	r.row.LeaseUntil = time.Now().Add(time.Minute)
	current := storage{client: c, owner: "current"}
	if raw, err := current.LoadSession(t.Context()); err != nil || string(raw) != "private authorization session" {
		t.Fatal("encrypted session could not be loaded")
	}
	stale := storage{client: c, owner: "old"}
	if err := stale.StoreSession(t.Context(), []byte("replacement")); err == nil {
		t.Fatal("stale worker replaced the account session")
	}
	cfg.Phone = "+37491234567"
	other, _ := New(cfg, r)
	if _, err := (storage{client: other, owner: "current"}).LoadSession(t.Context()); err == nil {
		t.Fatal("authorization session transferred to another account")
	}
}

func TestResolveRequiresExactUserPeerAndRejectsSelfOrBots(t *testing.T) {
	for _, tc := range []struct {
		name         string
		id, returned int64
		bot          bool
		want         bool
	}{
		{"client", 2, 2, false, true}, {"self", 1, 1, false, false}, {"different user", 2, 3, false, false}, {"bot", 2, 2, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer, err := resolvedUser(&tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: tc.id}, Users: []tg.UserClass{&tg.User{ID: tc.returned, AccessHash: 99, Bot: tc.bot}}}, 1)
			if (err == nil) != tc.want {
				t.Fatalf("unexpected resolved identity: %v %v", peer, err)
			}
		})
	}
}
