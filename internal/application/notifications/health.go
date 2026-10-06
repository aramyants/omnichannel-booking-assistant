package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/google/uuid"
)

// HealthGuard shares the last confirmed restrictions across Cloud Run instances.
// Probes are read-only, leased and capped at one per five minutes. No probe sends
// a customer message or changes the WhatsApp registration.
type HealthGuard struct {
	Repo  Repository
	Read  func(context.Context) (messaging.ChannelHealth, error)
	Alert func(context.Context, messaging.ChannelHealth) error
	Now   func() time.Time
}

func (g *HealthGuard) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *HealthGuard) Refresh(ctx context.Context) error {
	key, owner, now := Key("channel_health", "whatsapp"), uuid.NewString(), g.now()
	claimed := false
	if err := g.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		claimed = false
		row := rows[key]
		if row.LeaseUntil.After(now) || row.NextLookupAt.After(now) {
			return nil
		}
		row.Kind, row.LeaseOwner, row.LeaseUntil = "channel_health", owner, now.Add(time.Minute)
		claimed = true
		return nil
	}); err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	health, probeErr := g.Read(ctx)
	alert := false
	var final messaging.ChannelHealth
	err := g.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		alert = false
		row := rows[key]
		if row.LeaseOwner != owner {
			return nil
		}
		row.LeaseOwner, row.LeaseUntil = "", time.Time{}
		row.NextLookupAt = now.Add(5 * time.Minute)
		if probeErr != nil || !health.Known {
			return nil
		}
		previous := row.Health
		if !health.TemplatesKnown {
			health.Templates, health.TemplatesKnown = previous.Templates, previous.TemplatesKnown
		}
		health.CheckedAt = now
		before, after := healthFingerprint(previous), healthFingerprint(health)
		// Reserve the alert in the same transaction as the state change. An
		// ambiguous alert delivery must not spam staff on every scheduler tick.
		alert = before != after && (previous.Known || restrictedHealth(health))
		row.Health, row.UpdatedAt = health, now
		final = health
		return nil
	})
	if err != nil {
		return err
	}
	if alert && g.Alert != nil {
		if err := g.Alert(ctx, final); err != nil {
			return fmt.Errorf("notify WhatsApp health change: %w", err)
		}
	}
	return probeErr
}

func (g *HealthGuard) Allows(ctx context.Context, template, language string) (bool, error) {
	// A transient Graph lookup failure preserves the durable last-known state.
	// Without a confirmed restriction, the provider remains the authority for
	// delivery. Database failures must not silently bypass a stored restriction.
	row, err := g.Repo.GetNotification(ctx, Key("channel_health", "whatsapp"))
	if err != nil {
		return false, err
	}
	if !row.NextLookupAt.After(g.now()) {
		_ = g.Refresh(ctx)
		row, err = g.Repo.GetNotification(ctx, Key("channel_health", "whatsapp"))
		if err != nil {
			return false, err
		}
	}
	return row.Health.Allows(template, language), nil
}

func healthFingerprint(h messaging.ChannelHealth) string {
	var paused []string
	for key, approved := range h.Templates {
		if !approved {
			paused = append(paused, key)
		}
	}
	sort.Strings(paused)
	raw, _ := json.Marshal(struct {
		Blocked, Paused bool
		Templates       []string
	}{h.Blocked, h.ProactivePaused, paused})
	return string(raw)
}

func restrictedHealth(h messaging.ChannelHealth) bool {
	if h.Blocked || h.ProactivePaused {
		return true
	}
	for _, approved := range h.Templates {
		if !approved {
			return true
		}
	}
	return false
}
