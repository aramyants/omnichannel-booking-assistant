package meta

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Only smb_message_echoes represents manual Business app activity. Cloud API
// message_echoes and delivery statuses must not take conversations from the bot.
func parseWhatsAppExternalReplies(body []byte, receivedAt time.Time, phoneNumberID string) ([]messaging.ExternalReply, error) {
	if phoneNumberID == "" {
		return nil, nil // Never accept staff activity without an asset binding.
	}
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedUpdate, err)
	}
	if u.Object != "whatsapp_business_account" {
		return nil, nil
	}
	var replies []messaging.ExternalReply
	for _, entry := range u.Entry {
		for _, change := range entry.Changes {
			value := change.Value
			if change.Field != "smb_message_echoes" || value.MessagingProduct != "whatsapp" || value.Metadata.PhoneNumberID != phoneNumberID {
				continue
			}
			for _, echo := range value.MessageEchoes {
				if echo.Type == "edit" || echo.Type == "revoke" {
					// Updating previous transcript entries needs separate support.
					// Do not misrepresent these operations as newly sent messages.
					continue
				}
				seconds, err := strconv.ParseInt(echo.Timestamp, 10, 64)
				if err != nil || seconds <= 0 || echo.ID == "" || !whatsAppPhone(echo.From) || !whatsAppPhone(echo.To) || echo.From == echo.To {
					return nil, fmt.Errorf("%w: invalid business app echo", ErrMalformedUpdate)
				}
				sentAt := time.Unix(seconds, 0).UTC()
				if sentAt.After(receivedAt) {
					sentAt = receivedAt
				}
				content := whatsAppContent(echo.inboundMessage)
				if content.Type != messaging.ContentTypeText {
					// No outbound media download or transcription: this is context
					// about staff activity, never a request needing a bot answer.
					content = messaging.Content{Type: messaging.ContentTypeUnsupported, Text: "[Staff sent " + describeWhatsAppAttachment(echo.inboundMessage) + " from the WhatsApp Business app]"}
				}
				replies = append(replies, messaging.ExternalReply{
					Provider: messaging.ProviderWhatsApp, ExternalMessageID: echo.ID,
					ExternalThreadID: echo.To, Content: content, SentAt: sentAt, ReceivedAt: receivedAt,
				})
			}
		}
	}
	return replies, nil
}

func whatsAppPhone(value string) bool {
	return value != "" && strings.Trim(value, "0123456789") == ""
}
