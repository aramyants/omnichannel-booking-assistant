package meta

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// The complete label travels in the payload, never in a truncated UI title.
// The conversation validates the nonce against the last delivered prompt.
func choicePayload(token, label string) string {
	return "c:" + token + ":" + base64.RawURLEncoding.EncodeToString([]byte(label))
}

func decodeChoice(payload string) (token, label string) {
	parts := strings.SplitN(payload, ":", 3)
	if len(parts) != 3 || parts[0] != "c" || parts[1] == "" || len(payload) > 1000 {
		return "invalid", "Expired choice"
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !utf8.Valid(decoded) || len(decoded) == 0 {
		return "invalid", "Expired choice"
	}
	return parts[1], string(decoded)
}

func shortened(text string, limit int) string {
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit-1]) + "…"
}

func choiceTitle(label string, limit int) string {
	return shortened(strings.TrimSpace(label), limit)
}

type quickReply struct {
	ContentType string `json:"content_type"`
	Title       string `json:"title"`
	Payload     string `json:"payload"`
}

func quickReplies(msg messaging.Outgoing) []quickReply {
	var result []quickReply
	for i, choice := range msg.Choices {
		if i == 13 {
			break
		}
		payload := choicePayload(msg.ChoiceToken, choice.Label)
		if len(payload) > 1000 {
			continue
		}
		result = append(result, quickReply{"text", choiceTitle(choice.Label, 20), payload})
	}
	return result
}

func whatsAppInteractive(msg messaging.Outgoing) any {
	type row struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
	}
	rows := make([]row, 0, len(msg.Choices))
	for i, choice := range msg.Choices {
		if i == 10 {
			break
		}
		payload := choicePayload(msg.ChoiceToken, choice.Label)
		// Row IDs are limited to 200 characters. Large labels remain in the
		// numbered text fallback instead of causing the whole API request to fail.
		if len(payload) > 200 {
			continue
		}
		rows = append(rows, row{payload, choiceTitle(choice.Label, 24), ""})
	}
	if len(rows) == 0 {
		return nil
	}
	interactive := map[string]any{"type": "list", "body": map[string]string{"text": msg.Text},
		"action": map[string]any{"button": "Menu", "sections": []any{map[string]any{"rows": rows}}}}
	// Three or fewer short choices can be answered directly in the chat.
	if len(rows) <= 3 {
		var buttons []any
		for _, r := range rows {
			buttons = append(buttons, map[string]any{"type": "reply", "reply": map[string]string{"id": r.ID, "title": shortened(r.Title, 20)}})
		}
		interactive["type"] = "button"
		interactive["action"] = map[string]any{"buttons": buttons}
	}
	return map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": msg.ExternalThreadID, "type": "interactive", "interactive": interactive}
}

// Split on a line boundary when possible. Never split UTF-8 bytes or drop
// practical visit details to fit a provider's text limit.
func textChunks(text string, limit int) []string {
	var chunks []string
	for utf8.RuneCountInString(text) > limit {
		runes := []rune(text)
		end := limit
		for i := limit - 1; i > limit/2; i-- {
			if runes[i] == '\n' {
				end = i
				break
			}
		}
		chunks = append(chunks, string(runes[:end]))
		text = strings.TrimLeft(string(runes[end:]), "\n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
