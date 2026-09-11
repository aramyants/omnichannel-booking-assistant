package messaging

import (
	"fmt"
	"time"
)

// ExternalReply records a human message already delivered outside the bot.
// It must never be fed into Handle as if the customer had written it.
type ExternalReply struct {
	Provider          Provider
	ExternalMessageID string
	ExternalThreadID  string
	Content           Content
	SentAt            time.Time
	ReceivedAt        time.Time
}

func (r ExternalReply) Validate() error {
	if r.Provider != ProviderWhatsApp || r.ExternalMessageID == "" || r.ExternalThreadID == "" || r.Content.Type == "" || r.SentAt.IsZero() || r.ReceivedAt.IsZero() {
		return fmt.Errorf("%w: invalid external staff reply", ErrInvalidEnvelope)
	}
	return nil
}
