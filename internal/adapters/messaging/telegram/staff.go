package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// staffTranscriptLines is how much of the conversation is quoted to the
// colleague. Enough to see what the customer wants without turning a
// notification into a wall of text.
const staffTranscriptLines = 6

// staffMessageLimit keeps a notice inside Telegram's 4096 character limit,
// leaving room for the footer that is appended after truncation.
const staffMessageLimit = 3500

// StaffNotifier posts handover notices into a Telegram chat the business
// watches.
//
// A group chat is the right shape for this: everyone on shift sees it, whoever
// is free picks it up, and the history is a record of what was asked for.
type StaffNotifier struct {
	client *Client
	chatID string
}

// NewStaffNotifier returns a notifier posting to chatID.
func NewStaffNotifier(client *Client, chatID string) (*StaffNotifier, error) {
	if client == nil {
		return nil, fmt.Errorf("telegram: a client is required to notify staff")
	}
	if chatID == "" {
		return nil, fmt.Errorf("telegram: a staff chat id is required")
	}
	return &StaffNotifier{client: client, chatID: chatID}, nil
}

// NotifyHandoff tells the staff chat that a customer needs a person.
func (n *StaffNotifier) NotifyHandoff(ctx context.Context, notice assistant.HandoffNotice) error {
	return n.client.Send(ctx, messaging.Outgoing{
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: n.chatID,
		Text:             formatHandoff(notice),
	})
}

// formatHandoff writes the notice as something a person can act on from their
// phone without opening anything else.
func formatHandoff(notice assistant.HandoffNotice) string {
	var b strings.Builder

	if notice.Reason.Urgent() {
		b.WriteString("UNRESOLVED BOOKING - please check the calendar now\n\n")
	} else {
		b.WriteString("A customer is waiting for a person\n\n")
	}

	// Contact details first: the whole point is to reach this customer.
	if name := strings.TrimSpace(notice.Customer.Name); name != "" {
		fmt.Fprintf(&b, "Customer: %s\n", name)
	} else if notice.Handle != "" {
		fmt.Fprintf(&b, "Customer: %s\n", notice.Handle)
	}

	if phone := strings.TrimSpace(notice.Customer.Phone); phone != "" {
		fmt.Fprintf(&b, "Phone: %s\n", phone)
	}

	// A colleague reading this on their phone needs something they can act on.
	// A username is tappable; failing that, a direct link to the account works
	// even for somebody who has never set one, which is the common case.
	switch {
	case notice.Handle != "":
		fmt.Fprintf(&b, "Telegram: @%s\n", strings.TrimPrefix(notice.Handle, "@"))
	case notice.ExternalUserID != "":
		fmt.Fprintf(&b, "Open the chat: tg://user?id=%s\n", notice.ExternalUserID)
	default:
		fmt.Fprintf(&b, "Channel: %s\n", notice.Provider)
	}

	if detail := strings.TrimSpace(notice.Detail); detail != "" {
		fmt.Fprintf(&b, "\nWhy: %s\n", detail)
	}

	if draft := notice.Draft; draft != nil {
		fmt.Fprintf(&b, "\nAppointment in question: %s with %s, %s\n",
			strings.Join(draft.ServiceNames, ", "),
			draft.StaffName,
			draft.StartsAt.Format("2 Jan 2006 15:04"),
		)
		if draft.Phone != "" {
			fmt.Fprintf(&b, "Booked under: %s, %s\n", draft.CustomerName, draft.Phone)
		}
	}

	if transcript := formatTranscript(notice.Recent); transcript != "" {
		b.WriteString("\nLast messages:\n")
		b.WriteString(transcript)
	}

	text := b.String()
	if len(text) > staffMessageLimit {
		text = text[:staffMessageLimit] + "\n[truncated]\n"
	}

	// The assistant stays quiet until somebody replies or the timeout passes,
	// so the colleague needs to know the clock is running.
	text += fmt.Sprintf("\nThe assistant has stopped replying to this customer.\nConversation: %s",
		notice.ConversationID)

	return text
}

// formatTranscript renders the tail of the conversation, oldest first.
func formatTranscript(messages []conversation.Message) string {
	if len(messages) > staffTranscriptLines {
		messages = messages[len(messages)-staffTranscriptLines:]
	}

	var b strings.Builder
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}

		who := "Customer"
		if msg.Direction == conversation.DirectionOutbound {
			who = "Assistant"
		}
		fmt.Fprintf(&b, "  %s: %s\n", who, text)
	}
	return b.String()
}
