package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// MessageHandler receives normalised messages parsed from a webhook delivery.
//
// The interface is declared here, next to the code that calls it, so the
// adapter depends on a shape it needs rather than on a concrete service.
type MessageHandler interface {
	Handle(ctx context.Context, msg messaging.Envelope) error
}

// Handler serves Telegram webhook deliveries.
// StaffDesk is what a colleague's message in the staff chat acts on.
//
// The interface is declared here, beside the code that calls it, so the adapter
// depends on the shape it needs rather than on a concrete service.
type StaffDesk interface {
	// RelayStaffReply sends a colleague's message to the customer.
	RelayStaffReply(ctx context.Context, reply assistant.StaffReply) error

	// RunStaffCommand applies an instruction and returns what to say back in
	// the staff chat.
	RunStaffCommand(ctx context.Context, command assistant.StaffCommand, conversationID string) (string, error)
}

type Handler struct {
	webhook     *Webhook
	messages    MessageHandler
	logger      *slog.Logger
	staffChatID string

	// desk and threads are set when the staff chat is configured. Without them
	// a colleague can read a notification but replying to it does nothing.
	desk    StaffDesk
	threads StaffThreads

	// staffReplies posts acknowledgements back into the staff chat.
	staffReplies func(ctx context.Context, text string) error
}

// HandlerOption customises a Handler.
type HandlerOption func(*Handler)

// WithStaffChat names the chat the business is notified in.
//
// Adding the bot to a staff group means the group's own messages arrive at this
// same webhook. Without this, colleagues talking among themselves would each be
// treated as a customer and answered by the assistant.
func WithStaffChat(chatID string) HandlerOption {
	return func(h *Handler) { h.staffChatID = chatID }
}

// WithStaffDesk turns the staff chat from somewhere notifications are posted
// into somewhere colleagues answer customers.
func WithStaffDesk(desk StaffDesk, threads StaffThreads, client *Client, chatID string) HandlerOption {
	return func(h *Handler) {
		h.desk = desk
		h.threads = threads
		h.staffReplies = func(ctx context.Context, text string) error {
			return client.Send(ctx, messaging.Outgoing{
				Provider:         messaging.ProviderTelegram,
				ExternalThreadID: chatID,
				Text:             text,
			})
		}
	}
}

// NewHandler returns an http.Handler for the Telegram webhook endpoint.
func NewHandler(webhook *Webhook, messages MessageHandler, logger *slog.Logger, opts ...HandlerOption) *Handler {
	h := &Handler{webhook: webhook, messages: messages, logger: logger}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ServeHTTP verifies, parses and processes one webhook delivery.
//
// The status code chosen here decides whether Telegram redelivers, so the two
// failure modes are answered differently:
//
//   - A body that cannot be parsed will never parse, however many times it is
//     sent. It is logged and answered 200, because redelivering it forever
//     would be pure load.
//   - A failure to process a message that did parse may well be transient. It
//     is answered 500 so Telegram delivers it again.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !h.webhook.VerifySecret(r.Header.Get(SecretHeader)) {
		// Deliberately terse: an attacker probing the endpoint learns nothing
		// from the response, and the detail goes to the log instead.
		h.logger.WarnContext(ctx, "rejected a telegram delivery with an invalid secret",
			"remote_addr", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.ErrorContext(ctx, "could not read a telegram delivery", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	envelopes, err := h.webhook.Parse(body)
	if err != nil {
		// Logged at error because it means either a Telegram change or a bug
		// here, but answered 200 so it is not redelivered indefinitely.
		h.logger.ErrorContext(ctx, "discarded an unparseable telegram delivery",
			"error", err, "bytes", len(body))
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, envelope := range envelopes {
		// Colleagues talking in the staff group are not customers. Their
		// messages are either an answer for a customer, an instruction, or
		// ordinary chatter that is none of this system's business.
		if h.staffChatID != "" && envelope.ExternalThreadID == h.staffChatID {
			h.handleStaffMessage(ctx, body)
			continue
		}

		if err := h.messages.Handle(ctx, envelope); err != nil {
			if errors.Is(err, context.Canceled) {
				// The caller hung up or the process is shutting down. Telegram
				// will redeliver, which is the correct outcome.
				h.logger.WarnContext(ctx, "abandoned a telegram message mid-flight",
					"dedupe_key", envelope.DedupeKey())
			} else {
				h.logger.ErrorContext(ctx, "could not process a telegram message",
					"error", err, "dedupe_key", envelope.DedupeKey())
			}
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// handleStaffMessage acts on something a colleague wrote in the staff chat.
//
// Nothing here fails the delivery. A staff group is full of conversation that
// is not addressed to this system, and answering Telegram with an error would
// have it redeliver ordinary chatter forever.
func (h *Handler) handleStaffMessage(ctx context.Context, body []byte) {
	if h.desk == nil || h.threads == nil {
		h.logger.DebugContext(ctx, "ignored a staff message: no desk is configured")
		return
	}

	staff, ok := ParseStaffMessage(body)
	if !ok {
		return
	}

	// Only a reply names a conversation. Colleagues talking among themselves
	// are left alone.
	if staff.ReplyToMessageID == "" {
		if staff.IsCommand() {
			h.tellStaff(ctx, "Reply to the notification for the customer you mean, then send the command.")
		}
		return
	}

	conversationID, err := h.threads.ConversationForStaffThread(ctx, staff.ReplyToMessageID)
	if err != nil {
		h.logger.ErrorContext(ctx, "could not resolve which conversation a colleague replied to",
			"error", err, "staff_message_id", staff.ReplyToMessageID)
		h.tellStaff(ctx, "Sorry, I could not work out which customer that reply was for.")
		return
	}
	if conversationID == "" {
		// A reply to something that was never a customer notification.
		return
	}

	if staff.IsCommand() {
		h.runStaffCommand(ctx, staff, conversationID)
		return
	}

	if err := h.desk.RelayStaffReply(ctx, assistant.StaffReply{
		ConversationID: conversationID,
		AuthorName:     staff.AuthorName,
		Text:           staff.Text,
	}); err != nil {
		h.logger.ErrorContext(ctx, "could not relay a colleague's reply to the customer",
			"error", err, "conversation_id", conversationID)
		h.tellStaff(ctx, "That did not reach the customer. Please try again.")
	}
}

func (h *Handler) runStaffCommand(ctx context.Context, staff StaffMessage, conversationID string) {
	switch assistant.StaffCommand(staff.Command) {
	case assistant.CommandResume, assistant.CommandTake:
		answer, err := h.desk.RunStaffCommand(ctx,
			assistant.StaffCommand(staff.Command), conversationID)
		if err != nil {
			h.logger.ErrorContext(ctx, "could not apply a colleague's command",
				"error", err, "command", staff.Command, "conversation_id", conversationID)
			h.tellStaff(ctx, "That did not work: "+err.Error())
			return
		}
		h.tellStaff(ctx, answer)

		// A command can carry a message for the customer on the same line.
		if text := staff.StaffReplyText(); text != "" {
			if err := h.desk.RelayStaffReply(ctx, assistant.StaffReply{
				ConversationID: conversationID,
				AuthorName:     staff.AuthorName,
				Text:           text,
			}); err != nil {
				h.logger.ErrorContext(ctx, "could not relay the message sent with a command",
					"error", err, "conversation_id", conversationID)
			}
		}

	default:
		h.tellStaff(ctx, describeUnknownCommand(staff.Command))
	}
}

// tellStaff posts back into the staff chat. Failure is logged and nothing else:
// an acknowledgement that did not arrive must not fail the work it describes.
func (h *Handler) tellStaff(ctx context.Context, text string) {
	if h.staffReplies == nil {
		return
	}
	if err := h.staffReplies(ctx, text); err != nil {
		h.logger.ErrorContext(ctx, "could not answer in the staff chat", "error", err)
	}
}
