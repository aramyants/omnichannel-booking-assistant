// Package cabinet links private appointment access to proof of phone ownership.
package cabinet

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

var (
	ErrExpired     = errors.New("phone verification expired")
	ErrInvalid     = errors.New("phone verification code is invalid")
	ErrLimited     = errors.New("phone verification temporarily limited")
	ErrUnavailable = errors.New("phone verification unavailable")
)

type CodeSender interface {
	SendCode(context.Context, string, string, string, time.Time) error
}

type Identity struct {
	Repo   notifications.Repository
	Sender CodeSender
	Secret []byte
	Now    func() time.Time
}

func (s *Identity) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func key(provider messaging.Provider, user string) string {
	return notifications.Key("cabinet_identity", string(provider)+":"+user)
}
func challengeKey(provider messaging.Provider, user string) string {
	return notifications.Key("cabinet_challenge", string(provider)+":"+user)
}

// Phone accepts only authenticated provider addresses or previously completed
// ownership proof. Booking contact text and notification consent are separate.
func (s *Identity) Phone(ctx context.Context, provider messaging.Provider, user string) (string, error) {
	row, err := s.Repo.GetNotification(ctx, key(provider, user))
	if err != nil {
		return "", err
	}
	if row.Contact.ExpiresAt.After(s.now()) {
		return customer.NormalizePhone(row.Contact.Phone)
	}
	if provider == messaging.ProviderWhatsApp {
		phone, err := customer.NormalizePhone("+" + strings.TrimPrefix(user, "+"))
		if err != nil {
			return "", nil
		}
		return phone, nil
	}
	// Existing own-contact links have already passed the Telegram webhook and
	// contact.user_id == sender.id checks. Migrate lazily without another prompt.
	if provider == messaging.ProviderTelegram {
		old, err := s.Repo.GetNotification(ctx, notifications.Key("telegram_chat", user))
		if err != nil {
			return "", err
		}
		if old.Contact.ExpiresAt.After(s.now()) && old.Contact.Chat == user {
			return customer.NormalizePhone(old.Contact.Phone)
		}
	}
	return "", nil
}

func (s *Identity) Begin(ctx context.Context, provider messaging.Provider, user string) error {
	return s.Repo.TransactNotifications(ctx, []string{challengeKey(provider, user)}, func(rows map[string]*notifications.Entry) error {
		row := rows[challengeKey(provider, user)]
		if row.Verification.State == "code" && row.Verification.ExpiresAt.After(s.now()) {
			return nil
		}
		row.Kind = "cabinet_challenge"
		row.Verification.State = "phone"
		row.Verification.ExpiresAt = s.now().Add(10 * time.Minute)
		return nil
	})
}

func (s *Identity) Pending(ctx context.Context, provider messaging.Provider, user string) (string, error) {
	row, err := s.Repo.GetNotification(ctx, challengeKey(provider, user))
	if err != nil {
		return "", err
	}
	if !row.Verification.ExpiresAt.After(s.now()) {
		if row.Verification.State == "code" || row.Verification.State == "locked" {
			return "expired", nil
		}
		return "", nil
	}
	return row.Verification.State, nil
}

func (s *Identity) Cancel(ctx context.Context, provider messaging.Provider, user string) error {
	return s.Repo.TransactNotifications(ctx, []string{challengeKey(provider, user)}, func(rows map[string]*notifications.Entry) error {
		row := rows[challengeKey(provider, user)]
		row.Verification.State = ""
		row.Verification.Hash = nil
		return nil
	})
}

// RecordTelegramResolution is used only after Telegram's server has returned
// the exact user peer for this phone. It grants cabinet access, not bot notices.
func RecordTelegramResolution(ctx context.Context, repo notifications.Repository, user, phone string, now time.Time) error {
	phone, err := customer.NormalizePhone(phone)
	if err != nil || user == "" {
		return ErrInvalid
	}
	return repo.TransactNotifications(ctx, []string{key(messaging.ProviderTelegram, user)}, func(rows map[string]*notifications.Entry) error {
		*rows[key(messaging.ProviderTelegram, user)] = notifications.Entry{Kind: "cabinet_identity", Contact: notifications.Contact{Phone: phone, Chat: user, ExpiresAt: now.Add(180 * 24 * time.Hour)}}
		return nil
	})
}

// OwnContact runs only after the Telegram adapter checks the contact's owner.
func (s *Identity) OwnContact(ctx context.Context, user, phone string) error {
	state, err := s.Pending(ctx, messaging.ProviderTelegram, user)
	if err != nil {
		return err
	}
	if state != "phone" {
		return ErrExpired
	}
	if err := RecordTelegramResolution(ctx, s.Repo, user, phone, s.now()); err != nil {
		return err
	}
	return s.Cancel(ctx, messaging.ProviderTelegram, user)
}

func (s *Identity) digest(provider messaging.Provider, user, phone, code string) []byte {
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = fmt.Fprintf(mac, "cabinet-code:%s:%s:%s:%s", provider, user, phone, code)
	return mac.Sum(nil)
}

func (s *Identity) Send(ctx context.Context, provider messaging.Provider, user, rawPhone string) error {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return ErrInvalid
	}
	if s.Sender == nil || len(s.Secret) < 32 {
		return ErrUnavailable
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return ErrUnavailable
	}
	code := fmt.Sprintf("%06d", n.Int64())
	randomID := make([]byte, 16)
	if _, err := rand.Read(randomID); err != nil {
		return ErrUnavailable
	}
	ownKey := challengeKey(provider, user)
	phoneKey := notifications.Key("cabinet_phone_budget", phone)
	now := s.now()
	expiry := now.Add(5 * time.Minute)
	err = s.Repo.TransactNotifications(ctx, []string{ownKey, phoneKey}, func(rows map[string]*notifications.Entry) error {
		own, budget := &rows[ownKey].Verification, &rows[phoneKey].Verification
		if own.State != "phone" || !own.ExpiresAt.After(now) {
			return ErrExpired
		}
		for _, entry := range []*notifications.PhoneChallenge{own, budget} {
			if entry.NextSendAt.After(now) {
				return ErrLimited
			}
			if now.Sub(entry.WindowStarted) >= time.Hour {
				entry.WindowStarted = now
				entry.Sends = 0
			}
			if entry.Sends >= 5 {
				return ErrLimited
			}
		}
		for _, entry := range []*notifications.PhoneChallenge{own, budget} {
			entry.Sends++
			entry.NextSendAt = now.Add(time.Minute)
		}
		own.Phone = phone
		own.Hash = s.digest(provider, user, phone, code)
		own.State = "code"
		own.Attempts = 0
		own.ExpiresAt = expiry
		rows[phoneKey].Kind = "cabinet_phone_budget"
		return nil
	})
	if err != nil {
		return err
	}
	// One request, never retried on an uncertain SMSGate result.
	return s.Sender.SendCode(ctx, phone, "E-Motion Concept: "+code+" is your booking access code. Expires in 5 minutes. Do not share it.", hex.EncodeToString(randomID), expiry)
}

func (s *Identity) Confirm(ctx context.Context, provider messaging.Provider, user, code string) (string, error) {
	ownKey, proofKey := challengeKey(provider, user), key(provider, user)
	var phone string
	var result error
	err := s.Repo.TransactNotifications(ctx, []string{ownKey, proofKey}, func(rows map[string]*notifications.Entry) error {
		phone = ""
		result = nil
		v := &rows[ownKey].Verification
		if v.State != "code" || !v.ExpiresAt.After(s.now()) {
			result = ErrExpired
			return nil
		}
		if v.Attempts >= 5 {
			result = ErrLimited
			return nil
		}
		v.Attempts++
		if len(code) != 6 || !hmac.Equal(v.Hash, s.digest(provider, user, v.Phone, code)) {
			result = ErrInvalid
			if v.Attempts >= 5 {
				v.State = "locked"
				result = ErrLimited
			}
			return nil // Persist failed attempts, including across Cloud Run instances.
		}
		phone = v.Phone
		*rows[proofKey] = notifications.Entry{Kind: "cabinet_identity", Contact: notifications.Contact{Phone: phone, Chat: user, ExpiresAt: s.now().Add(180 * 24 * time.Hour)}}
		v.State = ""
		v.Hash = nil
		v.Phone = ""
		return nil
	})
	if err != nil {
		return "", err
	}
	return phone, result
}
