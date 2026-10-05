package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/staffinbox"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

type staffInbox struct {
	repository staffinbox.Repository
	client     *Client
	chatID     string
	location   *time.Location
	threads    StaffThreads
}

// WithStaffInbox enables staff-only history navigation. The handler checks the
// configured chat before any private history read or staff action.
func WithStaffInbox(repository staffinbox.Repository, client *Client, chatID string, location *time.Location) HandlerOption {
	return func(h *Handler) {
		if chatID == "" {
			return
		}
		if location == nil {
			location = time.UTC
		}
		h.inbox = &staffInbox{repository: repository, client: client, chatID: chatID, location: location, threads: h.threads}
	}
}

func inboxData(action, reference string) string { return "inbox:" + action + ":" + reference }

func (h *Handler) handleInboxCallback(ctx context.Context, callback Callback) bool {
	if !strings.HasPrefix(callback.Data, "inbox:") {
		return false
	}
	h.acknowledge(ctx, callback.QueryID, "")
	if h.inbox == nil || h.staffChatID == "" || callback.ChatID != h.staffChatID {
		return true
	}
	parts := strings.Split(callback.Data, ":")
	if len(parts) != 3 || len(parts[2]) > 36 || strings.ContainsAny(parts[2], "/\\") {
		return true
	}
	if err := h.inboxAction(ctx, parts[1], parts[2]); err != nil {
		h.logger.ErrorContext(ctx, "could not open staff inbox page", "error", err)
		h.tellStaff(ctx, "Չհաջողվեց բացել այս էջը։ Խնդրում ենք կրկին ուղարկել /inbox։")
	}
	return true
}

func (h *Handler) inboxAction(ctx context.Context, action, reference string) error {
	switch action {
	case "list":
		return h.inbox.list(ctx, nil)
	case "open":
		return h.inbox.history(ctx, reference, "", "")
	case "take", "resume":
		conv, err := h.inbox.repository.FindByID(ctx, reference)
		if err != nil || conv.Provider != messaging.ProviderTelegram {
			return fmt.Errorf("inbox: unavailable Telegram conversation")
		}
		if h.desk == nil {
			return fmt.Errorf("inbox: no staff desk")
		}
		if _, err := h.desk.RunStaffCommand(ctx, assistant.StaffCommand(action), reference); err != nil {
			return err
		}
		return h.inbox.history(ctx, reference, "", "")
	case "next", "older", "back":
		page, err := h.inbox.repository.InboxPage(ctx, reference)
		if err != nil {
			return err
		}
		if page.ChatID != h.staffChatID {
			return fmt.Errorf("inbox: page belongs to another chat")
		}
		if action == "next" && page.NextConversations != nil {
			return h.inbox.list(ctx, page.NextConversations)
		}
		if action == "older" && page.HistoryBefore != "" {
			return h.inbox.history(ctx, page.ConversationID, page.HistoryBefore, page.ID)
		}
		if action == "back" && page.PreviousPageID != "" {
			previous, err := h.inbox.repository.InboxPage(ctx, page.PreviousPageID)
			if err != nil || previous.ChatID != h.staffChatID || previous.ConversationID != page.ConversationID {
				return fmt.Errorf("inbox: previous page unavailable")
			}
			return h.inbox.history(ctx, previous.ConversationID, previous.HistoryStart, previous.PreviousPageID)
		}
	}
	return nil
}

func (i *staffInbox) list(ctx context.Context, cursor *staffinbox.Cursor) error {
	result, err := i.repository.TelegramConversations(ctx, cursor, staffinbox.ConversationLimit)
	if err != nil {
		return err
	}
	page := staffinbox.Page{ID: id.New(), ChatID: i.chatID, NextConversations: result.Next, ExpiresAt: time.Now().Add(staffinbox.PageRetention)}
	if err := i.repository.SaveInboxPage(ctx, page); err != nil {
		return err
	}
	text := "Հաճախորդների Telegram զրույցները\n\nԸնտրեք զրույցը՝ պատմությունը կարդալու կամ պատասխանելու համար։ Ժամերը՝ Երևանի ժամանակով։"
	keyboard := &inlineKeyboardMarkup{}
	for _, conv := range result.Items {
		cust, err := i.repository.InboxCustomer(ctx, conv.CustomerID)
		if err != nil {
			return err
		}
		label := shortInboxName(cust, conv) + " · " + conv.LastMessageAt.In(i.location).Format("02.01 15:04")
		switch conv.State {
		case conversation.StateHumanRequested:
			label = "Սպասում է · " + label
		case conversation.StateHumanActive:
			label = "Թիմը · " + label
		}
		keyboard.Keyboard = append(keyboard.Keyboard, []inlineKeyboardButton{{Text: label, CallbackData: inboxData("open", conv.ID)}})
	}
	if len(result.Items) == 0 {
		text += "\n\nԱյս էջում զրույցներ չկան։"
	}
	navigation := []inlineKeyboardButton{{Text: "Թարմացնել", CallbackData: inboxData("list", "")}}
	if result.Next != nil {
		navigation = append(navigation, inlineKeyboardButton{Text: "Հաջորդ էջը", CallbackData: inboxData("next", page.ID)})
	}
	keyboard.Keyboard = append(keyboard.Keyboard, navigation)
	_, err = i.client.sendWithMarkup(ctx, i.chatID, text, keyboard)
	return err
}

func shortInboxName(cust customer.Customer, conv conversation.Conversation) string {
	name := strings.Join(strings.Fields(cust.Name), " ")
	if name == "" {
		name = "Հաճախորդ " + conv.ExternalThreadID
	}
	runes := []rune(name)
	if len(runes) > 32 {
		name = string(runes[:32]) + "…"
	}
	return name
}

func (i *staffInbox) history(ctx context.Context, conversationID, before, previousPageID string) error {
	conv, err := i.repository.FindByID(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv.Provider != messaging.ProviderTelegram {
		return fmt.Errorf("inbox: not a Telegram conversation")
	}
	cust, err := i.repository.InboxCustomer(ctx, conv.CustomerID)
	if err != nil {
		return err
	}
	result, err := i.repository.InboxHistory(ctx, conversationID, before, staffinbox.HistoryLimit)
	if err != nil {
		return err
	}
	page := staffinbox.Page{ID: id.New(), ChatID: i.chatID, ConversationID: conversationID,
		HistoryBefore: result.Before, HistoryStart: before, PreviousPageID: previousPageID, ExpiresAt: time.Now().Add(staffinbox.PageRetention)}
	if err := i.repository.SaveInboxPage(ctx, page); err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n", shortInboxName(cust, conv))
	if cust.Phone != "" {
		fmt.Fprintf(&text, "Հեռախոս՝ %s\n", cust.Phone)
	}
	state := "Պատասխանում է օգնականը"
	switch conv.State {
	case conversation.StateHumanRequested:
		state = "Սպասում է թիմի պատասխանին"
	case conversation.StateHumanActive:
		state = "Պատասխանում է թիմը"
	case conversation.StateClosed:
		state = "Զրույցն ավարտված է"
	}
	fmt.Fprintf(&text, "%s\n\n", state)
	for _, msg := range result.Messages {
		who := "Հաճախորդ"
		if msg.Direction == conversation.DirectionOutbound {
			who = "Մեր թիմը / օգնականը"
		}
		body := msg.Text
		if strings.TrimSpace(body) == "" {
			body = "[" + string(msg.ContentType) + "]"
		}
		fmt.Fprintf(&text, "%s · %s\n%s\n\n", who, msg.CreatedAt.In(i.location).Format("02.01.2006 15:04"), body)
	}
	text.WriteString("Հաճախորդին պատասխանելու համար օգտագործեք Reply այս հաղորդագրության վրա։ Պատասխանը կուղարկվի ստուդիայի բոտից, և օգնականը կդադարի պատասխանել մինչև այն կրկին միացնեք։")
	navigation := []inlineKeyboardButton{}
	if result.Before != "" {
		navigation = append(navigation, inlineKeyboardButton{Text: "Ավելի վաղ", CallbackData: inboxData("older", page.ID)})
	}
	if previousPageID != "" {
		navigation = append(navigation, inlineKeyboardButton{Text: "Հետ", CallbackData: inboxData("back", page.ID)})
	}
	keyboard := &inlineKeyboardMarkup{}
	if len(navigation) > 0 {
		keyboard.Keyboard = append(keyboard.Keyboard, navigation)
	}
	keyboard.Keyboard = append(keyboard.Keyboard,
		[]inlineKeyboardButton{{Text: "Վերջին հաղորդագրությունները", CallbackData: inboxData("open", conversationID)}, {Text: "Զրույցների ցանկ", CallbackData: inboxData("list", "")}},
		[]inlineKeyboardButton{{Text: "Պատասխանել թիմով", CallbackData: inboxData("take", conversationID)}, {Text: "Միացնել օգնականին", CallbackData: inboxData("resume", conversationID)}},
	)
	chunks := splitInboxText(text.String(), 3500)
	for index, chunk := range chunks {
		var markup *inlineKeyboardMarkup
		if index == len(chunks)-1 {
			markup = keyboard
		}
		messageID, err := i.client.sendWithMarkup(ctx, i.chatID, chunk, markup)
		if err != nil {
			return err
		}
		if messageID == "" {
			return fmt.Errorf("inbox: Telegram returned no message id")
		}
		if err := i.threads.LinkStaffThread(ctx, messageID, conversationID); err != nil {
			return err
		}
	}
	return nil
}

// Telegram's limit is UTF-16 code units. Splitting at rune boundaries preserves
// complete Armenian text and emoji, without silently truncating client history.
func splitInboxText(text string, limit int) []string {
	var chunks []string
	start, units := 0, 0
	for offset, r := range text {
		size := utf16.RuneLen(r)
		if units+size > limit {
			chunks = append(chunks, text[start:offset])
			start, units = offset, 0
		}
		units += size
	}
	if start < len(text) {
		chunks = append(chunks, text[start:])
	}
	return chunks
}
