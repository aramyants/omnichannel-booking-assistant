package notifications

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestHealthGuardSharedRestrictionTransientFailureAndRecovery(t *testing.T) {
	now := time.Now().UTC()
	repo := &testRepository{rows: map[string]Entry{}}
	state := messaging.ChannelHealth{Known: true, TemplatesKnown: true, Templates: map[string]bool{"booking:en": true}}
	var probeErr error
	var calls, alerts atomic.Int32
	newGuard := func() *HealthGuard {
		return &HealthGuard{Repo: repo, Now: func() time.Time { return now },
			Read:  func(context.Context) (messaging.ChannelHealth, error) { calls.Add(1); return state, probeErr },
			Alert: func(context.Context, messaging.ChannelHealth) error { alerts.Add(1); return nil },
		}
	}
	first, second := newGuard(), newGuard()
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			guard := first
			if i%2 == 0 {
				guard = second
			}
			if err := guard.Refresh(t.Context()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || alerts.Load() != 0 {
		t.Fatalf("healthy baseline probes=%d alerts=%d", calls.Load(), alerts.Load())
	}
	now = now.Add(6 * time.Minute)
	state.Blocked = true
	if allowed, err := second.Allows(t.Context(), "", ""); err != nil || allowed {
		t.Fatalf("blocked reply allowed=%v err=%v", allowed, err)
	}
	if allowed, err := first.Allows(t.Context(), "booking", "en"); err != nil || allowed {
		t.Fatalf("blocked template allowed=%v err=%v", allowed, err)
	}
	if alerts.Load() != 1 {
		t.Fatalf("restriction alerts=%d", alerts.Load())
	}
	now = now.Add(6 * time.Minute)
	state.Blocked, probeErr = false, errors.New("temporary Graph failure")
	if allowed, err := first.Allows(t.Context(), "", ""); err != nil || allowed {
		t.Fatalf("transient failure erased restriction: %v %v", allowed, err)
	}
	if alerts.Load() != 1 {
		t.Fatal("transient probe repeated alert")
	}
	now = now.Add(6 * time.Minute)
	probeErr = nil
	if allowed, err := second.Allows(t.Context(), "", ""); err != nil || !allowed {
		t.Fatalf("recovery unavailable: %v %v", allowed, err)
	}
	if alerts.Load() != 2 {
		t.Fatalf("recovery alerts=%d", alerts.Load())
	}
}

func TestPausedTemplateLeavesRepliesAndOtherLanguageAvailable(t *testing.T) {
	repo := &testRepository{rows: map[string]Entry{}}
	guard := &HealthGuard{Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
		return messaging.ChannelHealth{Known: true, TemplatesKnown: true, Templates: map[string]bool{"booking:en": false, "booking_ru:ru": true}}, nil
	}}
	for _, test := range []struct {
		template, lang string
		allowed        bool
	}{{"", "", true}, {"booking", "en", false}, {"booking_ru", "ru", true}} {
		if got, err := guard.Allows(t.Context(), test.template, test.lang); err != nil || got != test.allowed {
			t.Fatalf("%s:%s allowed=%v err=%v", test.template, test.lang, got, err)
		}
	}
}

func TestHealthAlertFailureDoesNotRepeatOnEveryCheck(t *testing.T) {
	now := time.Now()
	var alerts int
	guard := &HealthGuard{Repo: &testRepository{rows: map[string]Entry{}}, Now: func() time.Time { return now },
		Read: func(context.Context) (messaging.ChannelHealth, error) {
			return messaging.ChannelHealth{Known: true, Blocked: true}, nil
		},
		Alert: func(context.Context, messaging.ChannelHealth) error {
			alerts++
			return errors.New("uncertain alert delivery")
		},
	}
	if err := guard.Refresh(t.Context()); err == nil {
		t.Fatal("lost alert error")
	}
	now = now.Add(6 * time.Minute)
	if err := guard.Refresh(t.Context()); err != nil || alerts != 1 {
		t.Fatalf("alert repeated=%d error=%v", alerts, err)
	}
}

func TestPausedBookingTemplateUsesEligibleSMSWithoutWhatsAppAttempt(t *testing.T) {
	for _, consent := range []bool{true, false} {
		s, repo, reader, sender := notificationFixture(t)
		s.SMSReady, sender.reject = true, true
		if consent {
			if err := s.AllowPhone(t.Context(), reader.snapshot.Phone, "hy"); err != nil {
				t.Fatal(err)
			}
		}
		s.Health = &HealthGuard{Repo: repo, Read: func(context.Context) (messaging.ChannelHealth, error) {
			return messaging.ChannelHealth{Known: true, TemplatesKnown: true, Templates: map[string]bool{"approved_fixture:en": false}}, nil
		}}
		id := ingestAndDeliver(t, s, "create")
		if err := s.Deliver(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		want := []Channel{Telegram}
		if consent {
			want = append(want, SMS)
		}
		if !reflect.DeepEqual(sender.channels, want) {
			t.Fatalf("consent=%v routes=%v want=%v", consent, sender.channels, want)
		}
	}
}
