package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://graph.facebook.com"

	// DefaultGraphVersion is the Graph API version calls are made against.
	// Meta retires versions on a schedule, so it is configurable: moving to the
	// next one is a configuration change rather than a release.
	DefaultGraphVersion = "v22.0"

	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 1 << 20
)

// Errors the Graph API can report, in terms the application acts on.
var (
	// ErrUnavailable means Meta could not be reached or failed in a way that
	// may succeed later.
	ErrUnavailable = errors.New("meta unavailable")

	// ErrRejected means Meta refused the request for a reason that repeating
	// it will not fix.
	ErrRejected = errors.New("meta rejected the request")

	// ErrOutsideServiceWindow means WhatsApp will not deliver a free-form
	// message because the customer has not written for 24 hours.
	//
	// This is a rule of the platform, not a fault. Re-opening the conversation
	// requires an approved template, so a colleague has to be told rather than
	// the message being retried.
	ErrOutsideServiceWindow = errors.New("outside the 24 hour customer service window")
)

// Client calls the Meta Graph API.
type Client struct {
	httpClient    *http.Client
	baseURL       string
	version       string
	accessToken   string
	phoneNumberID string
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL overrides the Graph host. Tests point this at a local server.
func WithBaseURL(rawURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(rawURL, "/") }
}

// WithHTTPClient overrides the HTTP client, and with it the transport and
// timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithGraphVersion selects the Graph API version.
func WithGraphVersion(version string) Option {
	return func(c *Client) {
		if version != "" {
			c.version = version
		}
	}
}

// NewClient returns a client for one Meta app.
//
// phoneNumberID is the WhatsApp number messages are sent from. It is empty for
// an app serving only Instagram or Messenger.
func NewClient(accessToken, phoneNumberID string, opts ...Option) (*Client, error) {
	if accessToken == "" {
		return nil, errors.New("meta: access token is required")
	}

	c := &Client{
		httpClient:    &http.Client{Timeout: defaultTimeout},
		baseURL:       defaultBaseURL,
		version:       DefaultGraphVersion,
		accessToken:   accessToken,
		phoneNumberID: phoneNumberID,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("meta %s: encode request: %w", path, err)
	}

	url := c.baseURL + "/" + c.version + "/" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("meta %s: build request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The token goes in a header rather than the query string, where it would
	// be written to every access log between here and Meta.
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("meta %s: %w: %w", path, ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("meta %s: %w: read response: %w", path, ErrUnavailable, err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return c.translate(path, resp.StatusCode, raw)
}

// translate turns a Graph failure into an error the application can act on.
func (c *Client) translate(path string, status int, raw []byte) error {
	var parsed graphError
	_ = json.Unmarshal(raw, &parsed)

	message := parsed.Error.Message
	if message == "" {
		message = http.StatusText(status)
	}
	if parsed.Error.UserMsg != "" {
		// Meta's user-facing text is usually the one that explains the cause.
		message = parsed.Error.UserMsg
	}

	// 131047 is WhatsApp's code for a free-form message sent more than 24 hours
	// after the customer last wrote. It needs a person, not a retry.
	if parsed.Error.Code == 131047 || parsed.Error.Subcode == 131047 {
		return fmt.Errorf("meta %s: %w: %s", path, ErrOutsideServiceWindow, message)
	}

	switch {
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		return fmt.Errorf("meta %s: %w: %s", path, ErrUnavailable, message)
	default:
		return fmt.Errorf("meta %s: %w: %s", path, ErrRejected, message)
	}
}
