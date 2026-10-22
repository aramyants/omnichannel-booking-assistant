package meta

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func parseDirectExternalReplies(body []byte, receivedAt time.Time, provider messaging.Provider, accountID string) ([]messaging.ExternalReply, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	object := "page"
	if provider == messaging.ProviderInstagram {
		object = "instagram"
	}
	if accountID == "" || u.Object != object {
		return nil, nil
	}
	var replies []messaging.ExternalReply
	for _, entry := range u.Entry {
		if entry.ID != accountID {
			continue
		}
		for _, event := range entry.Messaging {
			m := event.Message
			if m == nil || !m.IsEcho || m.IsDeleted || event.Sender.ID != accountID || event.Recipient.ID == "" || event.Recipient.ID == accountID {
				continue
			}
			// Other integrations' automated API sends also must not impersonate staff.
			if len(m.AppID) > 0 && string(m.AppID) != "null" && string(m.AppID) != "0" && string(m.AppID) != "\"\"" {
				continue
			}
			if m.MID == "" || event.Timestamp <= 0 {
				return nil, fmt.Errorf("%w: incomplete outbound echo", ErrMalformedUpdate)
			}
			at := time.UnixMilli(event.Timestamp).UTC()
			if at.After(receivedAt) {
				at = receivedAt
			}
			content := messaging.Content{Type: messaging.ContentTypeText, Text: m.Text}
			if content.Text == "" {
				content = messaging.Content{Type: messaging.ContentTypeUnsupported, Text: "[Administrator sent an attachment from the business inbox]"}
			}
			replies = append(replies, messaging.ExternalReply{Provider: provider, ExternalMessageID: m.MID, ExternalThreadID: event.Recipient.ID, Content: content, SentAt: at, ReceivedAt: receivedAt})
		}
	}
	return replies, nil
}
