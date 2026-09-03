package telegram

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Callback data prefixes. Telegram gives 64 bytes for the whole string, and an
// Armenian service name spends that on about twenty characters, so nothing but
// a reference travels in it.
const (
	// choicePrefix marks a customer's answer. What follows is the button's
	// position in the keyboard, not its text: the text is read back out of the
	// keyboard Telegram returns with the press.
	choicePrefix = "c:"

	// staffPrefix marks a colleague acting on a notification. What follows is
	// the action and the conversation it applies to, because a staff chat holds
	// many notifications and the press has to name one.
	staffPrefix = "s:"
)

// Rows are sized by how long the labels are: times fit three abreast, service
// names do not fit two.
const (
	shortLabelRunes  = 8
	mediumLabelRunes = 18
)

// keyboardFor lays choices out as an inline keyboard, or returns nil when
// there is nothing to offer.
func keyboardFor(choices []messaging.Choice) *inlineKeyboardMarkup {
	if len(choices) == 0 {
		return nil
	}

	perRow := buttonsPerRow(choices)
	markup := &inlineKeyboardMarkup{}

	var row []inlineKeyboardButton
	for i, choice := range choices {
		row = append(row, inlineKeyboardButton{
			Text:         choice.Label,
			CallbackData: choicePrefix + strconv.Itoa(i),
		})
		if len(row) == perRow {
			markup.Keyboard = append(markup.Keyboard, row)
			row = nil
		}
	}
	if len(row) > 0 {
		markup.Keyboard = append(markup.Keyboard, row)
	}

	return markup
}

// buttonsPerRow picks a width the longest label still fits in. One row of three
// truncated labels is worse than three rows of readable ones.
func buttonsPerRow(choices []messaging.Choice) int {
	longest := 0
	for _, choice := range choices {
		if n := utf8.RuneCountInString(choice.Label); n > longest {
			longest = n
		}
	}

	switch {
	case longest <= shortLabelRunes:
		return 3
	case longest <= mediumLabelRunes:
		return 2
	default:
		return 1
	}
}

// labelForCallback returns the text of the button that was pressed.
//
// It is read out of the keyboard Telegram sends back with the press rather than
// from anything stored here, which is what lets the callback data stay inside
// its 64 bytes while the label says whatever it needs to in whatever alphabet.
//
// It reports false for a press this system cannot make sense of: a keyboard
// from a deployment that no longer exists, or a message whose markup Telegram
// did not include.
func labelForCallback(markup *inlineKeyboardMarkup, data string) (string, bool) {
	if markup == nil || !strings.HasPrefix(data, choicePrefix) {
		return "", false
	}

	for _, row := range markup.Keyboard {
		for _, button := range row {
			if button.CallbackData != data {
				continue
			}
			label := strings.TrimSpace(button.Text)
			if label == "" {
				return "", false
			}
			return label, true
		}
	}
	return "", false
}

// staffAction is what a colleague pressed on a notification.
type staffAction struct {
	Command        string
	ConversationID string
}

// staffActionData builds the callback payload for one button on a staff
// notification.
func staffActionData(command, conversationID string) string {
	return staffPrefix + command + ":" + conversationID
}

// parseStaffAction reads a staff button press, reporting false for anything
// that is not one.
func parseStaffAction(data string) (staffAction, bool) {
	rest, ok := strings.CutPrefix(data, staffPrefix)
	if !ok {
		return staffAction{}, false
	}

	command, conversationID, found := strings.Cut(rest, ":")
	if !found || command == "" || conversationID == "" {
		return staffAction{}, false
	}
	return staffAction{Command: command, ConversationID: conversationID}, true
}
