// Package telegramaccount sends appointment notices as an authorized studio
// user account. This is separate from the Telegram Bot API and AI conversations.
package telegramaccount

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const storageKey = "telegram_studio_account"

type Config struct {
	AppID                    int
	AppHash, Phone, Username string
	EncryptionKey            []byte
}
type Client struct {
	config Config
	repo   notifications.Repository
	aead   cipher.AEAD
}

func New(cfg Config, repo notifications.Repository) (*Client, error) {
	phone, err := customer.NormalizePhone(cfg.Phone)
	if err != nil || cfg.AppID <= 0 || cfg.AppHash == "" || cfg.Username == "" || len(cfg.EncryptionKey) != 32 || repo == nil {
		return nil, errors.New("invalid studio Telegram account configuration")
	}
	cfg.Phone = phone
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Client{config: cfg, repo: repo, aead: aead}, nil
}

// InstallSession is used only by the owner-side commissioning tool, after Self
// verified the studio identity. The gateway has no public enrollment endpoint.
func (c *Client) InstallSession(ctx context.Context, raw []byte) error {
	if len(raw) == 0 {
		return errors.New("empty studio session")
	}
	encrypted, err := c.encrypt(raw)
	if err != nil {
		return err
	}
	return c.repo.TransactNotifications(ctx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
		row := rows[storageKey]
		if row.LeaseUntil.After(time.Now()) {
			return notifications.ErrBusy
		}
		row.Kind = "account_session"
		row.SessionCiphertext = encrypted
		row.LeaseOwner = ""
		row.LeaseUntil = time.Time{}
		row.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (c *Client) encrypt(raw []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, raw, []byte(c.config.Phone)), nil
}

type storage struct {
	client *Client
	owner  string
}

// CheckConnection verifies encrypted cloud storage and the authorized identity
// without resolving any customer phone, reading history or sending a message.
func (c *Client) CheckConnection(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	owner := uuid.NewString()
	err := c.repo.TransactNotifications(ctx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
		row := rows[storageKey]
		if len(row.SessionCiphertext) == 0 {
			return errors.New("studio account not connected")
		}
		if row.LeaseUntil.After(time.Now()) {
			return notifications.ErrBusy
		}
		row.LeaseOwner = owner
		row.LeaseUntil = time.Now().UTC().Add(time.Minute)
		return nil
	})
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = c.repo.TransactNotifications(ctx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
			if rows[storageKey].LeaseOwner == owner {
				rows[storageKey].LeaseUntil = time.Time{}
			}
			return nil
		})
	}()
	store := storage{client: c, owner: owner}
	if _, err = store.LoadSession(ctx); err != nil {
		return errors.New("encrypted studio session unavailable")
	}
	client := telegram.NewClient(c.config.AppID, c.config.AppHash, telegram.Options{SessionStorage: store, NoUpdates: true})
	err = client.Run(ctx, func(ctx context.Context) error {
		own, err := client.Self(ctx)
		if err != nil {
			return err
		}
		if own.Bot || "+"+strings.TrimPrefix(own.Phone, "+") != c.config.Phone || !strings.EqualFold(own.Username, c.config.Username) {
			return errors.New("studio identity mismatch")
		}
		return nil
	})
	if err != nil {
		return errors.New("studio account connection unavailable")
	}
	return nil
}

func (s storage) LoadSession(ctx context.Context) ([]byte, error) {
	row, err := s.client.repo.GetNotification(ctx, storageKey)
	if err != nil {
		return nil, err
	}
	if row.LeaseOwner != s.owner || !row.LeaseUntil.After(time.Now()) {
		return nil, notifications.ErrBusy
	}
	data := row.SessionCiphertext
	n := s.client.aead.NonceSize()
	if len(data) < n {
		return nil, session.ErrNotFound
	}
	return s.client.aead.Open(nil, data[:n], data[n:], []byte(s.client.config.Phone))
}
func (s storage) StoreSession(ctx context.Context, raw []byte) error {
	encrypted, err := s.client.encrypt(raw)
	if err != nil {
		return err
	}
	return s.client.repo.TransactNotifications(ctx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
		row := rows[storageKey]
		if row.LeaseOwner != s.owner || !row.LeaseUntil.After(time.Now()) {
			return notifications.ErrBusy
		}
		row.SessionCiphertext = encrypted
		row.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// Send returns rejected=true only when no send was attempted or Telegram
// explicitly rejected it. Lookup privacy errors mean unavailable, not absent.
func (c *Client) Send(ctx context.Context, phone, text, noticeID string) (bool, error) {
	phone, err := customer.NormalizePhone(phone)
	if err != nil || phone == c.config.Phone || strings.TrimSpace(text) == "" || len(text) > 12000 || noticeID == "" {
		return true, errors.New("invalid studio notification recipient")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	owner := uuid.NewString()
	now := time.Now().UTC()
	var wait time.Duration
	err = c.repo.TransactNotifications(ctx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
		row := rows[storageKey]
		if len(row.SessionCiphertext) == 0 {
			return errors.New("studio account not connected")
		}
		if row.LeaseUntil.After(now) {
			return notifications.ErrBusy
		}
		wait = row.NextLookupAt.Sub(now)
		if wait < 0 {
			wait = 0
		}
		if wait > 10*time.Second {
			return errors.New("studio account cooling down")
		}
		row.LeaseOwner = owner
		row.LeaseUntil = now.Add(time.Minute)
		row.NextLookupAt = now.Add(wait + 3*time.Second)
		return nil
	})
	if err != nil {
		return true, errors.New("studio account temporarily unavailable")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = c.repo.TransactNotifications(releaseCtx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
			if rows[storageKey].LeaseOwner == owner {
				rows[storageKey].LeaseUntil = time.Time{}
			}
			return nil
		})
	}()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-timer.C:
		}
	}
	store := storage{client: c, owner: owner}
	// Loading first prevents accidentally creating a fresh unauthorized session.
	if _, err = store.LoadSession(ctx); err != nil {
		return true, errors.New("studio session unavailable")
	}
	client := telegram.NewClient(c.config.AppID, c.config.AppHash, telegram.Options{SessionStorage: store, NoUpdates: true, Device: telegram.DeviceConfig{DeviceModel: "E-Motion booking notifications", SystemVersion: "Cloud booking worker", AppVersion: "1.0", SystemLangCode: "en", LangCode: "en"}})
	attempted, accepted, rejected := false, false, true
	err = client.Run(ctx, func(ctx context.Context) error {
		own, e := client.Self(ctx)
		if e != nil {
			return e
		}
		if own.Bot || "+"+strings.TrimPrefix(own.Phone, "+") != c.config.Phone || !strings.EqualFold(own.Username, c.config.Username) {
			return errors.New("studio account identity mismatch")
		}
		resolved, e := client.API().ContactsResolvePhone(ctx, strings.TrimPrefix(phone, "+"))
		if e != nil {
			return e
		}
		peer, e := resolvedUser(resolved, own.ID)
		if e != nil {
			return e
		}
		requirements, e := client.API().UsersGetRequirementsToContact(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peer.UserID, AccessHash: peer.AccessHash}})
		if e != nil || len(requirements) != 1 {
			return errors.New("telegram contact requirements unavailable")
		}
		if _, ok := requirements[0].(*tg.RequirementToContactEmpty); !ok {
			return errors.New("telegram recipient requires paid or restricted contact")
		}
		attempted = true
		rejected = false
		_, e = client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: text, RandomID: randomID(noticeID, c.config.Phone), NoWebpage: true})
		if e == nil {
			accepted = true
			return nil
		}
		var rpc *tgerr.Error
		if errors.As(e, &rpc) && rpc.Code >= 400 && rpc.Code < 500 {
			rejected = true
		}
		return e
	})
	if accepted {
		return false, nil
	}
	if err == nil {
		err = errors.New("studio notification did not complete")
	}
	if delay, ok := tgerr.AsFloodWait(err); ok {
		cooldownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = c.repo.TransactNotifications(cooldownCtx, []string{storageKey}, func(rows map[string]*notifications.Entry) error {
			row := rows[storageKey]
			if row.LeaseOwner == owner {
				row.NextLookupAt = time.Now().UTC().Add(delay)
			}
			return nil
		})
	}
	if !attempted || rejected {
		return true, errors.New("telegram studio route could not accept this notice")
	}
	return false, errors.New("telegram studio send outcome uncertain")
}

func randomID(noticeID, phone string) int64 {
	sum := sha256.Sum256([]byte(phone + ":" + noticeID))
	value, _ := strconv.ParseInt(hex.EncodeToString(sum[:8])[:15], 16, 64)
	if value == 0 {
		return 1
	}
	return value
}
func resolvedUser(resolved *tg.ContactsResolvedPeer, self int64) (*tg.InputPeerUser, error) {
	if resolved == nil {
		return nil, errors.New("telegram phone unavailable")
	}
	peer, ok := resolved.Peer.(*tg.PeerUser)
	if !ok || peer.UserID == self {
		return nil, errors.New("telegram phone is not a customer user")
	}
	for _, user := range resolved.Users {
		u, ok := user.(*tg.User)
		if ok && u.ID == peer.UserID && !u.Bot && !u.Deleted && u.AccessHash != 0 {
			return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, nil
		}
	}
	return nil, errors.New("telegram user identity unavailable")
}
