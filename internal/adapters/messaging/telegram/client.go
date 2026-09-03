package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const (
	defaultBaseURL = "https://api.telegram.org"
	defaultTimeout = 10 * time.Second

	// maxResponseBytes caps what is read from a response. Telegram replies are
	// small; an unbounded read would let a compromised or misbehaving endpoint
	// exhaust memory.
	maxResponseBytes = 1 << 20
)

// APIError is a call Telegram rejected.
type APIError struct {
	Method      string
	StatusCode  int
	Code        int
	Description string

	// RetryAfter is how long Telegram asked the caller to wait. It is set only
	// on rate-limit responses.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram %s: %d %s (retry after %s)", e.Method, e.Code, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Retryable reports whether repeating the call could succeed. A rejected token
// or a chat the bot was blocked from will fail identically every time, and
// retrying those only wastes the rate limit.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

// Client calls the Telegram Bot API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// ClientOption customises a Client.
type ClientOption func(*Client)

// WithBaseURL overrides the API host. Tests point this at a local server.
func WithBaseURL(rawURL string) ClientOption {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(rawURL, "/") }
}

// WithHTTPClient overrides the HTTP client, and with it the transport and
// timeout.
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = h }
}

// NewClient returns a client authenticated with a bot token.
//
// The client does not retry. Deciding whether a failed send should be repeated
// belongs to the caller that knows whether the work is still worth doing, and
// retrying inside the client would hide that decision.
func NewClient(token string, opts ...ClientOption) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    defaultBaseURL,
		token:      token,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Send delivers a message to a Telegram chat.
func (c *Client) Send(ctx context.Context, msg messaging.Outgoing) error {
	if msg.Provider != messaging.ProviderTelegram {
		return fmt.Errorf("telegram client cannot deliver to %s", msg.Provider)
	}
	if err := msg.Validate(); err != nil {
		return err
	}

	_, err := c.SendReturningID(ctx, msg)
	return err
}

// SendReturningID delivers a message and reports the id Telegram gave it.
//
// The id is what makes a conversation out of a notification: a colleague
// replying to that message produces an update carrying this id, which is how
// the reply is matched back to the customer it concerns.
func (c *Client) SendReturningID(ctx context.Context, msg messaging.Outgoing) (string, error) {
	if msg.Provider != messaging.ProviderTelegram {
		return "", fmt.Errorf("telegram client cannot deliver to %s", msg.Provider)
	}
	if err := msg.Validate(); err != nil {
		return "", err
	}

	result, err := c.call(ctx, "sendMessage", sendMessageRequest{
		ChatID:      msg.ExternalThreadID,
		Text:        msg.Text,
		ReplyMarkup: keyboardFor(msg.Choices),
	})
	if err != nil {
		return "", err
	}

	// A message that was delivered but whose id could not be read is still
	// delivered. Losing the id only costs the ability to thread replies to it.
	var sent sentMessage
	if err := json.Unmarshal(result, &sent); err != nil || sent.MessageID == 0 {
		return "", nil
	}
	return strconv.FormatInt(sent.MessageID, 10), nil
}

// sendWithMarkup delivers a message under a keyboard this package built itself.
//
// It is separate from Send because those buttons stand for an action rather
// than for words a customer could have typed, and so carry a reference in their
// callback data instead of standing for their own label.
func (c *Client) sendWithMarkup(
	ctx context.Context,
	chatID, text string,
	markup *inlineKeyboardMarkup,
) (string, error) {
	result, err := c.call(ctx, "sendMessage", sendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: markup,
	})
	if err != nil {
		return "", err
	}

	var sent sentMessage
	if err := json.Unmarshal(result, &sent); err != nil || sent.MessageID == 0 {
		return "", nil
	}
	return strconv.FormatInt(sent.MessageID, 10), nil
}

// setWebhookRequest registers the endpoint Telegram delivers updates to.
type setWebhookRequest struct {
	URL            string   `json:"url"`
	SecretToken    string   `json:"secret_token"`
	AllowedUpdates []string `json:"allowed_updates"`
	MaxConnections int      `json:"max_connections,omitempty"`
}

// SetWebhook points Telegram at callbackURL and tells it to send secret with
// every delivery.
//
// The service registers its own webhook at startup so that deploying it is the
// only step needed to make it reachable, and so a changed URL cannot be left
// pointing at a previous deployment.
//
// allowed_updates is restricted to what the assistant acts on: subscribing to
// update types it ignores only costs bandwidth and log noise. Button presses
// are among them, and a webhook registered without callback_query would leave
// every keyboard this system sends dead on the screen.
func (c *Client) SetWebhook(ctx context.Context, callbackURL, secret string) error {
	_, err := c.call(ctx, "setWebhook", setWebhookRequest{
		URL:            callbackURL,
		SecretToken:    secret,
		AllowedUpdates: []string{"message", "callback_query"},
		MaxConnections: 40,
	})
	return err
}

// AnswerCallback acknowledges a button press.
//
// Telegram spins the button until this arrives and then shows the customer a
// failure, so it is called for every press including ones this system cannot
// act on. text, when given, appears as a brief toast over the chat.
func (c *Client) AnswerCallback(ctx context.Context, callbackQueryID, text string) error {
	_, err := c.call(ctx, "answerCallbackQuery", answerCallbackQueryRequest{
		CallbackQueryID: callbackQueryID,
		Text:            text,
	})
	return err
}

// ClearKeyboard removes the buttons under a message.
//
// A question that has been answered must stop being answerable: without this a
// customer can scroll back a week and tap a time that was free then, and a
// colleague can hand back a conversation somebody else already picked up.
func (c *Client) ClearKeyboard(ctx context.Context, chatID string, messageID int64) error {
	_, err := c.call(ctx, "editMessageReplyMarkup", editMessageReplyMarkupRequest{
		ChatID:    chatID,
		MessageID: messageID,
	})
	return err
}

// SetCommands publishes the menu shown beside the text box.
//
// scope is a chat id to publish the menu only there, or empty for the default
// menu every customer sees. Nothing about a command is enforced by publishing
// it: the menu is a hint to the customer, and the code that receives the
// command is what decides what it does.
func (c *Client) SetCommands(ctx context.Context, scope string, commands []Command) error {
	entries := make([]botCommand, 0, len(commands))
	for _, command := range commands {
		entries = append(entries, botCommand{
			Command:     strings.TrimPrefix(command.Name, "/"),
			Description: command.Description,
		})
	}

	req := setMyCommandsRequest{Commands: entries}
	if scope != "" {
		req.Scope = &commandScope{Type: "chat", ChatID: scope}
	}

	_, err := c.call(ctx, "setMyCommands", req)
	return err
}

// Command is one entry in the menu Telegram shows beside the text box.
type Command struct {
	Name        string
	Description string
}

func (c *Client) call(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: encode request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), bytes.NewReader(body))
	if err != nil {
		return nil, c.redact(fmt.Errorf("telegram %s: build request: %w", method, err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.redact(fmt.Errorf("telegram %s: %w", method, err))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, c.redact(fmt.Errorf("telegram %s: read response: %w", method, err))
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, &APIError{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Description: fmt.Sprintf("unreadable response with status %d", resp.StatusCode),
		}
	}

	if !parsed.OK {
		apiErr := &APIError{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Code:        parsed.ErrorCode,
			Description: parsed.Description,
		}
		if parsed.Parameters != nil && parsed.Parameters.RetryAfter > 0 {
			apiErr.RetryAfter = time.Duration(parsed.Parameters.RetryAfter) * time.Second
		}
		return nil, apiErr
	}

	return parsed.Result, nil
}

func (c *Client) methodURL(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// redact wraps err so that printing it cannot disclose the bot token.
//
// Telegram carries the token in the request path, so any transport error
// carrying a URL carries the credential with it, and those errors are logged.
func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	return &redactedError{err: err, secret: c.token}
}

// redactedError hides a secret in the printed form of an error while leaving
// the original reachable to errors.Is and errors.As, so callers can still test
// for context.DeadlineExceeded and the like.
type redactedError struct {
	err    error
	secret string
}

func (e *redactedError) Error() string {
	if e.secret == "" {
		return e.err.Error()
	}
	return strings.ReplaceAll(e.err.Error(), e.secret, "[REDACTED]")
}

func (e *redactedError) Unwrap() error { return e.err }
