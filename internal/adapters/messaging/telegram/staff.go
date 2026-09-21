package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
// watches. The group is deliberately a notification stream, not a second
// customer inbox: staff use the channel link in each notice to answer from the
// native business inbox where the full conversation and customer identity live.
type StaffThreads interface {
	LinkStaffThread(ctx context.Context, staffMessageID, conversationID string) error
	ConversationForStaffThread(ctx context.Context, staffMessageID string) (string, error)
}

type StaffNotifier struct {
	client  *Client
	threads StaffThreads

	mu     sync.Mutex
	chatID string
}

// NewStaffNotifier returns a notifier posting to chatID.
func NewStaffNotifier(client *Client, chatID string, threads StaffThreads) (*StaffNotifier, error) {
	if client == nil {
		return nil, fmt.Errorf("telegram: a client is required to notify staff")
	}
	if chatID == "" {
		return nil, fmt.Errorf("telegram: a staff chat id is required")
	}
	return &StaffNotifier{client: client, chatID: chatID, threads: threads}, nil
}

// NotifyHandoff tells the staff chat that a customer needs a person.
func (n *StaffNotifier) NotifyHandoff(ctx context.Context, notice assistant.HandoffNotice) error {
	text := formatHandoff(notice)
	buttons := handoffButtons(notice.ConversationID)

	_, err := n.client.sendWithMarkup(ctx, n.chat(), text, buttons)
	if migrated := MigratedChatID(err); migrated != "" {
		// The group was upgraded to a supergroup while this process ran. The
		// notice is the urgent part, so it follows the group to its new id
		// rather than being lost until somebody edits the configuration.
		n.moveTo(migrated)
		_, err = n.client.sendWithMarkup(ctx, migrated, text, buttons)
	}
	return err
}

func (n *StaffNotifier) chat() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.chatID
}

func (n *StaffNotifier) moveTo(chatID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.chatID = chatID
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

	writeChannel(&b, notice)

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

	text += "\nThe assistant has paused for this customer." +
		"\nOpen the channel link above and reply in the native business inbox." +
		"\nThis group is notifications only; replies here are not sent to customers." +
		"\nWhen the conversation is finished, use the button below to return it to the assistant."

	return text
}

// handoffButtons keeps one one-shot lifecycle action. Staff answer in the
// native inbox; when finished they return the conversation to automation. The
// handler removes this keyboard immediately after it is pressed.
func handoffButtons(conversationID string) *inlineKeyboardMarkup {
	if conversationID == "" {
		return nil
	}
	return &inlineKeyboardMarkup{Keyboard: [][]inlineKeyboardButton{{{
		Text:         "Done — return to assistant",
		CallbackData: staffActionData(string(assistant.CommandResume), conversationID),
	}}}}
}

// Meta Business Suite inboxes. The Page and Instagram account are the ones the
// signed-in colleague manages, so no account id is needed in the link.
const (
	messengerInboxURL = "https://business.facebook.com/latest/inbox/messenger"
	instagramInboxURL = "https://business.facebook.com/latest/inbox/instagram"
)

// writeChannel says where the customer is and gives a link that opens the chat.
//
// A colleague reading this on their phone needs something they can act on, and
// the link has to belong to the customer's own channel: a Telegram link for a
// Messenger customer opens nothing.
func writeChannel(b *strings.Builder, notice assistant.HandoffNotice) {
	switch notice.Provider {
	case messaging.ProviderMessenger:
		b.WriteString("Channel: Facebook Messenger\n")
		fmt.Fprintf(b, "Open the inbox: %s\n", messengerInboxURL)
	case messaging.ProviderInstagram:
		b.WriteString("Channel: Instagram\n")
		fmt.Fprintf(b, "Open the inbox: %s\n", instagramInboxURL)
	case messaging.ProviderWhatsApp:
		b.WriteString("Channel: WhatsApp\n")
		// WhatsApp identifies a customer by their number, so the account id is
		// itself the link.
		if digits := digitsOnly(notice.ExternalUserID); digits != "" {
			fmt.Fprintf(b, "Open the chat: https://wa.me/%s\n", digits)
		} else if digits := digitsOnly(notice.Customer.Phone); digits != "" {
			fmt.Fprintf(b, "Open the chat: https://wa.me/%s\n", digits)
		}
	default:
		// Telegram. A username is tappable; failing that, a direct link to the
		// account works even for somebody who has never set one, which is the
		// common case.
		switch {
		case notice.Handle != "":
			fmt.Fprintf(b, "Open the chat: https://t.me/%s\n", strings.TrimPrefix(notice.Handle, "@"))
		case notice.ExternalUserID != "":
			fmt.Fprintf(b, "Open the chat: tg://user?id=%s\n", notice.ExternalUserID)
		default:
			fmt.Fprintf(b, "Channel: %s\n", notice.Provider)
		}
	}
}

func digitsOnly(value string) string {
	var digits strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	return digits.String()
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
