package cabinet_test

import (
	"context"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"strings"
	"sync"
	"testing"
	"time"
)

type codeSender struct {
	code  string
	sends int
	err   error
}

func (s *codeSender) SendCode(_ context.Context, _ string, text, _ string, _ time.Time) error {
	s.sends++
	s.code = strings.Split(text, " ")[2]
	return s.err
}
func identityFixture() (*cabinet.Identity, *codeSender, *time.Time) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sender := &codeSender{}
	return &cabinet.Identity{Repo: memory.New(), Sender: sender, Secret: []byte(strings.Repeat("s", 32)), Now: func() time.Time { return now }}, sender, &now
}
func TestPhoneProofIsBoundSingleUseAndExpiring(t *testing.T) {
	s, sender, now := identityFixture()
	p := messaging.ProviderInstagram
	if phone, err := s.Phone(t.Context(), p, "alice"); err != nil || phone != "" {
		t.Fatal("unknown account was verified")
	}
	if err := s.Send(t.Context(), p, "alice", "+37491123456"); !errors.Is(err, cabinet.ErrExpired) {
		t.Fatal("unsolicited code send accepted")
	}
	if err := s.Begin(t.Context(), p, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), p, "alice", "+37491123456"); err != nil {
		t.Fatal(err)
	}
	if phone, err := s.Confirm(t.Context(), messaging.ProviderMessenger, "alice", sender.code); !errors.Is(err, cabinet.ErrExpired) || phone != "" {
		t.Fatal("code crossed a channel")
	}
	if phone, err := s.Confirm(t.Context(), p, "bob", sender.code); !errors.Is(err, cabinet.ErrExpired) || phone != "" {
		t.Fatal("code crossed an account")
	}
	if phone, err := s.Confirm(t.Context(), p, "alice", sender.code); err != nil || phone != "+37491123456" {
		t.Fatal("correct proof failed")
	}
	if _, err := s.Confirm(t.Context(), p, "alice", sender.code); !errors.Is(err, cabinet.ErrExpired) {
		t.Fatal("code reused")
	}
	if phone, err := s.Phone(t.Context(), p, "alice"); err != nil || phone != "+37491123456" {
		t.Fatal("proof not persisted")
	}
	*now = now.Add(181 * 24 * time.Hour)
	if phone, err := s.Phone(t.Context(), p, "alice"); err != nil || phone != "" {
		t.Fatal("expired proof granted access")
	}
}
func TestFailedAttemptsAndPhoneBudgetPersist(t *testing.T) {
	s, sender, now := identityFixture()
	p := messaging.ProviderMessenger
	_ = s.Begin(t.Context(), p, "alice")
	if err := s.Send(t.Context(), p, "alice", "+37491123456"); err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if sender.code == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		_, err := s.Confirm(t.Context(), p, "alice", wrong)
		if err == nil {
			t.Fatal("wrong code granted access")
		}
	}
	if _, err := s.Confirm(t.Context(), p, "alice", sender.code); !errors.Is(err, cabinet.ErrExpired) {
		t.Fatal("locked code accepted")
	}
	_ = s.Begin(t.Context(), messaging.ProviderInstagram, "bob")
	if err := s.Send(t.Context(), messaging.ProviderInstagram, "bob", "+37491123456"); !errors.Is(err, cabinet.ErrLimited) {
		t.Fatal("cross-channel phone rate limit bypassed")
	}
	if sender.sends != 1 {
		t.Fatal("duplicate SMS sent")
	}
	*now = now.Add(6 * time.Minute)
	if _, err := s.Confirm(t.Context(), p, "alice", sender.code); !errors.Is(err, cabinet.ErrExpired) {
		t.Fatal("expired code accepted")
	}
}
func TestConcurrentSMSRequestsSendOnlyOnce(t *testing.T) {
	s, sender, _ := identityFixture()
	_ = s.Begin(t.Context(), messaging.ProviderInstagram, "alice")
	var group sync.WaitGroup
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = s.Send(t.Context(), messaging.ProviderInstagram, "alice", "+37491123456")
		}()
	}
	group.Wait()
	if sender.sends != 1 {
		t.Fatalf("%d SMS requests, expected one", sender.sends)
	}
}
func TestUncertainSMSSendIsNotRetried(t *testing.T) {
	s, sender, _ := identityFixture()
	sender.err = errors.New("unknown send outcome")
	_ = s.Begin(t.Context(), messaging.ProviderInstagram, "alice")
	if err := s.Send(t.Context(), messaging.ProviderInstagram, "alice", "+37491123456"); err == nil {
		t.Fatal("uncertain delivery reported as sent")
	}
	if err := s.Send(t.Context(), messaging.ProviderInstagram, "alice", "+37491123456"); err == nil {
		t.Fatal("uncertain send retried")
	}
	if sender.sends != 1 {
		t.Fatal("SMS sent twice")
	}
}
func TestOwnTelegramContactAndAccountResolutionDoNotOptIntoNotices(t *testing.T) {
	s, _, now := identityFixture()
	if err := s.OwnContact(t.Context(), "123", "+37491123456"); !errors.Is(err, cabinet.ErrExpired) {
		t.Fatal("unrequested contact accepted")
	}
	_ = s.Begin(t.Context(), messaging.ProviderTelegram, "123")
	if err := s.OwnContact(t.Context(), "123", "+37491123456"); err != nil {
		t.Fatal(err)
	}
	if phone, err := s.Phone(t.Context(), messaging.ProviderTelegram, "123"); err != nil || phone != "+37491123456" {
		t.Fatal("own contact failed")
	}
	if err := cabinet.RecordTelegramResolution(t.Context(), s.Repo, "456", "+37499123456", *now); err != nil {
		t.Fatal(err)
	}
	if phone, err := s.Phone(t.Context(), messaging.ProviderTelegram, "456"); err != nil || phone != "+37499123456" {
		t.Fatal("studio sender resolution not linked")
	}
}
