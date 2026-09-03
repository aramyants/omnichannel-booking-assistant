package telegram

import "encoding/json"

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

	// CallbackQuery is a button press. It arrives instead of a message, and
	// carries the message the button was attached to, which is how the tapped
	// button is turned back into the words it stood for.
	CallbackQuery *callbackQuery `json:"callback_query"`
}

// callbackQuery is one press of an inline-keyboard button.
type callbackQuery struct {
	ID      string   `json:"id"`
	From    *user    `json:"from"`
	Message *message `json:"message"`

	// Data is what the button carried. Telegram caps it at 64 bytes, which no
	// service name or Armenian sentence fits inside, so it holds a position in
	// the keyboard rather than the label itself.
	Data string `json:"data"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      *user  `json:"from"`
	Chat      *chat  `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	Caption   string `json:"caption"`

	// ReplyToMessage is set when this message answers another. In the staff
	// chat it is how a colleague's reply is tied to the notification, and so
	// to the customer it concerns.
	ReplyToMessage *message `json:"reply_to_message"`

	// ReplyMarkup is the keyboard shown under this message. Telegram sends it
	// back with every callback query, which is what makes a button press
	// resolvable to its label without this system storing the keyboard it sent.
	ReplyMarkup *inlineKeyboardMarkup `json:"reply_markup"`

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

	// ReplyMarkup is omitted when there is nothing to offer, because sending
	// an empty keyboard leaves a blank strip under the message.
	ReplyMarkup *inlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// inlineKeyboardMarkup is a grid of buttons shown under a message.
type inlineKeyboardMarkup struct {
	Keyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

type inlineKeyboardButton struct {
	Text string `json:"text"`

	// CallbackData is what Telegram sends back when the button is pressed.
	CallbackData string `json:"callback_data"`
}

// answerCallbackQueryRequest acknowledges a button press.
//
// Telegram shows a loading indicator on the button until this is called, and
// gives up after a few seconds with an error the customer can see. It is not
// optional politeness.
type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
}

// editMessageReplyMarkupRequest replaces the keyboard under a message. Sent
// with no markup it removes one, which is how an answered question stops being
// answerable a second time.
type editMessageReplyMarkupRequest struct {
	ChatID      string                `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	ReplyMarkup *inlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// botCommand is one entry in the menu Telegram shows beside the text box.
type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type setMyCommandsRequest struct {
	Commands []botCommand `json:"commands"`

	// LanguageCode scopes the menu to customers whose app is set to that
	// language. Empty is the default menu, shown to everyone else.
	LanguageCode string `json:"language_code,omitempty"`

	// Scope keeps the customer menu out of the staff group, which needs a
	// different set of commands entirely.
	Scope *commandScope `json:"scope,omitempty"`
}

// commandScope names where a command menu applies.
type commandScope struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id,omitempty"`
}

// apiResponse is the envelope Telegram wraps every method result in.
type apiResponse struct {
	OK bool `json:"ok"`

	// Result is the method's payload. It is read only where the id of a
	// message this system sent is needed later, such as a staff notification a
	// colleague will reply to.
	Result json.RawMessage `json:"result"`

	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// sentMessage is the part of a sendMessage result this system keeps: the id of
// the message it just posted, so a reply to it can be recognised later.
type sentMessage struct {
	MessageID int64 `json:"message_id"`
}
