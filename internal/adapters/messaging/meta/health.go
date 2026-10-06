package meta

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type healthStatus struct {
	CanSend  string `json:"can_send_message"`
	Entities []struct {
		ID      string `json:"id"`
		Type    string `json:"entity_type"`
		CanSend string `json:"can_send_message"`
	} `json:"entities"`
}

// ReadWhatsAppHealth checks messaging health only. SIP/calling restrictions and
// a LIMITED phone with a pending display name do not disable customer replies.
// expected maps template name:language to true, derived from runtime configuration.
func (c *Client) ReadWhatsAppHealth(ctx context.Context, expected map[string]bool) (messaging.ChannelHealth, error) {
	var phone struct {
		Health  healthStatus `json:"health_status"`
		Quality string       `json:"quality_rating"`
	}
	if err := c.get(ctx, c.phoneNumberID, url.Values{"fields": {"health_status,quality_rating"}}, &phone); err != nil {
		return messaging.ChannelHealth{}, err
	}
	switch phone.Health.CanSend {
	case "AVAILABLE", "LIMITED", "BLOCKED":
	default:
		return messaging.ChannelHealth{}, errors.New("meta did not report messaging health")
	}
	health := messaging.ChannelHealth{Known: true, Blocked: phone.Health.CanSend == "BLOCKED", ProactivePaused: phone.Quality == "RED"}
	waba := ""
	for _, entity := range phone.Health.Entities {
		if entity.CanSend == "BLOCKED" {
			health.Blocked = true
		}
		if entity.Type == "WABA" {
			waba = entity.ID
		}
	}
	if health.Blocked || len(expected) == 0 || waba == "" {
		return health, nil
	}
	// A template lookup failure leaves its previous confirmed state in place.
	// Customer-initiated replies do not depend on template management availability.
	approved, err := c.readUtilityTemplates(ctx, waba, expected)
	if err == nil {
		health.Templates, health.TemplatesKnown = approved, true
	}
	return health, nil
}

func (c *Client) readUtilityTemplates(ctx context.Context, waba string, expected map[string]bool) (map[string]bool, error) {
	approved := make(map[string]bool, len(expected))
	for key := range expected {
		approved[key] = false
	}
	query := url.Values{"fields": {"name,language,status,category"}, "limit": {"100"}}
	for range 10 {
		var page struct {
			Data   []struct{ Name, Language, Status, Category string } `json:"data"`
			Paging struct {
				Next    string `json:"next"`
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
			} `json:"paging"`
		}
		if err := c.get(ctx, waba+"/message_templates", query, &page); err != nil {
			return nil, err
		}
		for _, t := range page.Data {
			key := t.Name + ":" + t.Language
			if expected[key] {
				approved[key] = t.Status == "APPROVED" && t.Category == "UTILITY"
			}
		}
		if page.Paging.Next == "" {
			return approved, nil
		}
		if strings.TrimSpace(page.Paging.Cursors.After) == "" {
			break
		}
		query.Set("after", page.Paging.Cursors.After)
	}
	return nil, errors.New("meta template listing did not complete")
}

// SetDeliveryGuard is called once during startup, before serving requests.
// Empty template/language identifies a customer-service reply or read/typing ack.
func (c *Client) SetDeliveryGuard(guard func(context.Context, string, string) (bool, error)) {
	c.deliveryGuard = guard
}
