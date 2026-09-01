package telegram

// The types in this file mirror the Telegram Bot API wire format. They are
// unexported on purpose: nothing outside this package should depend on how
// Telegram happens to shape its payloads.
//
// Only the fields the assistant acts on are declared. Unknown fields are
// ignored by encoding/json, so Telegram can add to the payload without
// breaking parsing here.

// update is one entry from the webhook. Exactly one of its optional members is
// populated per delivery.
type update struct {
	UpdateID      int64    `json:"update_id"`
	Message       *message `json:"message"`
	EditedMessage *message `json:"edited_message"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      *user  `json:"from"`
	Chat      *chat  `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	Caption   string `json:"caption"`

	// Attachment members, declared only so an unreadable message can be
	// described accurately to the customer rather than silently dropped.
	Photo     []photoSize `json:"photo"`
	Voice     *fileRef    `json:"voice"`
	Audio     *fileRef    `json:"audio"`
	Video     *fileRef    `json:"video"`
	VideoNote *fileRef    `json:"video_note"`
	Document  *fileRef    `json:"document"`
	Sticker   *fileRef    `json:"sticker"`
	Location  *location   `json:"location"`
	Contact   *contact    `json:"contact"`
}

type user struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type photoSize struct {
	FileID string `json:"file_id"`
}

type fileRef struct {
	FileID string `json:"file_id"`
}

type location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type contact struct {
	PhoneNumber string `json:"phone_number"`
}

// sendMessageRequest is the body of a sendMessage call.
//
// parse_mode is deliberately omitted. Telegram would then interpret Markdown or
// HTML in the text, which turns any stray character in a service name or a
// customer's own words into a formatting error or an injection point.
type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// apiResponse is the envelope Telegram wraps every method result in.
type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}
