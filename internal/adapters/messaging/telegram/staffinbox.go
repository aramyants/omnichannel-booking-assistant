package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// StaffMessage is something a colleague wrote in the staff chat.
type StaffMessage struct {
	// AuthorName is who wrote it, shown to the customer when the message is
	// relayed so they know a person has taken over.
	AuthorName string

	Text string

	// ReplyToMessageID is the staff-chat message being answered, when the
	// colleague used reply. It is empty for ordinary chatter, which is most of
	// what a staff group contains.
	ReplyToMessageID string

	// Command is the instruction the message carries, without its slash, when
	// it is one.
	Command string
}

// IsCommand reports whether the colleague issued an instruction rather than
// writing to a customer.
func (m StaffMessage) IsCommand() bool { return m.Command != "" }

// ParseStaffMessage reads a delivery from the staff chat.
//
// It returns false for anything that is not a message a colleague typed, such
// as somebody joining the group.
func ParseStaffMessage(body []byte) (StaffMessage, bool) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil || u.Message == nil {
		return StaffMessage{}, false
	}

	m := u.Message
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if text == "" {
		return StaffMessage{}, false
	}

	staff := StaffMessage{Text: text}
	if m.From != nil {
		staff.AuthorName = displayName(m.From)
	}
	if m.ReplyToMessage != nil {
		staff.ReplyToMessageID = strconv.FormatInt(m.ReplyToMessage.MessageID, 10)
	}

	// A command is the first word when it starts with a slash. Telegram
	// appends @botname in groups, which is not part of the command.
	if strings.HasPrefix(text, "/") {
		word := strings.Fields(text)[0]
		word = strings.TrimPrefix(word, "/")
		if at := strings.IndexByte(word, '@'); at >= 0 {
			word = word[:at]
		}
		staff.Command = strings.ToLower(word)
	}

	return staff, true
}

// StaffReplyText is the message with any command word removed, which is what a
// customer should receive.
func (m StaffMessage) StaffReplyText() string {
	if !m.IsCommand() {
		return m.Text
	}

	fields := strings.Fields(m.Text)
	if len(fields) <= 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(fields[1:], " "))
}

// describeUnknownCommand is what to say when a colleague types an instruction
// that does not exist, so a typo is answered rather than silently ignored.
func describeUnknownCommand(command string) string {
	return fmt.Sprintf("There is no /%s. Reply to a notification to answer the customer, "+
		"or send /resume on one to hand it back to the assistant.", command)
}
