package telegram

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// These calls improve feedback but must never add the normal API timeout to a
// booking response. Telegram expires typing after five seconds, so refresh just
// before then while the application is still working.
const (
	interactionTimeout = time.Second
	typingRefresh      = 4 * time.Second
	maxMessageRunes    = 4096
)

// Optional capabilities keep existing webhook consumers and button-only
// adapters compatible. The production Client supports both automatically.
type chatActivity interface {
	SendTyping(ctx context.Context, chatID string) error
}

type selectionFeedback interface {
	ShowSelection(ctx context.Context, chatID string, messageID int64, text string) error
}

func (h *Handler) beginTyping(ctx context.Context, chatID string) func() {
	return h.beginTypingEvery(ctx, chatID, typingRefresh)
}

func (h *Handler) beginTypingEvery(ctx context.Context, chatID string, refresh time.Duration) func() {
	activity, ok := h.buttons.(chatActivity)
	if !ok || chatID == "" {
		return func() {}
	}

	typingCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(refresh)
		defer ticker.Stop()
		for {
			if typingCtx.Err() != nil {
				return
			}
			callCtx, finish := context.WithTimeout(typingCtx, interactionTimeout)
			err := activity.SendTyping(callCtx, chatID)
			finish()
			if err != nil {
				if typingCtx.Err() == nil {
					h.logger.DebugContext(ctx, "could not show telegram typing status", "error", err)
				}
				// A failed cosmetic action does not fail a message or repeatedly
				// hit Telegram while it is unavailable or rate limiting us.
				return
			}
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func (h *Handler) showSelection(ctx context.Context, callback Callback) {
	feedback, ok := h.buttons.(selectionFeedback)
	text := selectedQuestion(callback.MessageText, callback.Envelope.Content.Text)
	if ok && text != "" {
		feedbackCtx, cancel := context.WithTimeout(ctx, interactionTimeout)
		err := feedback.ShowSelection(feedbackCtx, callback.ChatID, callback.MessageID, text)
		cancel()
		if err == nil {
			return
		}
		h.logger.DebugContext(ctx, "could not show telegram button selection", "error", err)
	}

	// Telegram may refuse edits to an old or missing message. The answer still
	// reaches the application, and removing its keyboard remains worth trying.
	h.clearKeyboard(ctx, callback.ChatID, callback.MessageID)
}

func selectedQuestion(question, label string) string {
	question, label = strings.TrimSpace(question), strings.TrimSpace(label)
	if question == "" || label == "" {
		return ""
	}
	receipt := "\n\n✓ " + label
	if strings.HasSuffix(question, receipt) {
		return question
	}
	text := question + receipt
	if utf8.RuneCountInString(text) > maxMessageRunes {
		// Preserve the existing question in full if adding a receipt would
		// exceed Telegram's edit limit. Keyboard removal still works.
		return ""
	}
	return text
}
