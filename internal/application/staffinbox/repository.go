// Package staffinbox defines the bounded history reads used by the staff inbox.
package staffinbox

import (
	"context"
	"errors"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

const (
	ConversationLimit = 8
	HistoryLimit      = 5
	PageRetention     = 30 * 24 * time.Hour
)

var ErrPageExpired = errors.New("inbox page expired")

// Cursor preserves the boundary displayed on a page, even if its last
// conversation receives another message before staff request the next page.
type Cursor struct {
	LastMessageAt time.Time
	Key           string
}

type Conversations struct {
	Items []conversation.Conversation
	Next  *Cursor
}

type History struct {
	// Messages are in reading order, oldest first.
	Messages []conversation.Message
	Before   string
}

// Page is a durable navigation reference. Callbacks contain only its UUID,
// keeping both contact details and long cursors out of Telegram callback data.
type Page struct {
	ID                string
	ChatID            string
	ConversationID    string
	NextConversations *Cursor
	HistoryBefore     string
	HistoryStart      string
	PreviousPageID    string
	ExpiresAt         time.Time
}

type Repository interface {
	TelegramConversations(ctx context.Context, before *Cursor, limit int) (Conversations, error)
	FindByID(ctx context.Context, conversationID string) (conversation.Conversation, error)
	InboxCustomer(ctx context.Context, customerID string) (customer.Customer, error)
	InboxHistory(ctx context.Context, conversationID, before string, limit int) (History, error)
	SaveInboxPage(ctx context.Context, page Page) error
	InboxPage(ctx context.Context, pageID string) (Page, error)
}
