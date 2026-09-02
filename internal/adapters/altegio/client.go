// Package altegio adapts the Altegio scheduling API to the application's
// booking port. Altegio request and response shapes do not leave this package.
package altegio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

const (
	defaultBaseURL = "https://api.alteg.io/api/v1"
	defaultTimeout = 15 * time.Second

	// Altegio allows 200 requests per minute and 5 per second per IP address.
	// Four per second stays under both, including the per-minute ceiling, and
	// leaves room for anything else running against the same account.
	requestsPerSecond = 4
	requestBurst      = 4

	// maxResponseBytes caps what is read from a response, so a misbehaving or
	// compromised endpoint cannot exhaust memory.
	maxResponseBytes = 4 << 20

	// defaultMaxAttempts counts the first try. Retries apply only to requests
	// that can be repeated safely.
	defaultMaxAttempts = 3
)

// errRequestRejected distinguishes a well-authenticated 4xx response from a
// credentials failure. Booking endpoints may translate the former into "slot
// unavailable"; treating the latter that way would hide a deployment fault as
// a customer scheduling race.
var errRequestRejected = errors.New("altegio request rejected")

// Client calls the Altegio API.
//
// The client is safe for concurrent use, and its rate limiter is shared across
// all callers, which is the point: the limit Altegio enforces is per IP, not
// per goroutine.
type Client struct {
	httpClient   *http.Client
	baseURL      string
	partnerToken string
	userToken    string
	companyID    string
	limiter      *rate.Limiter
	logger       *slog.Logger
	maxAttempts  int
	sleep        func(context.Context, time.Duration) error

	// location is the business's timezone. Appointment times are stored and
	// compared in UTC everywhere else in the system, but a customer saying
	// "Friday at three" means three o'clock where the salon is, and a slot
	// Altegio reports without an offset can only be read in this location.
	location *time.Location

	// currency labels the prices Altegio returns, which carry no currency of
	// their own.
	currency string
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL overrides the API host. Tests point this at a local server.
func WithBaseURL(rawURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(rawURL, "/") }
}

// WithHTTPClient overrides the HTTP client, and with it the transport and
// timeout.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithMaxAttempts overrides how many times a retryable request is tried,
// counting the first attempt.
func WithMaxAttempts(attempts int) Option {
	return func(c *Client) { c.maxAttempts = attempts }
}

// WithRateLimit overrides the request rate. Tests set it high to avoid waiting.
func WithRateLimit(perSecond, burst int) Option {
	return func(c *Client) { c.limiter = rate.NewLimiter(rate.Limit(perSecond), burst) }
}

// WithSleep overrides how backoff waits, so tests do not spend real time in it.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// WithLocation sets the business's timezone. It defaults to UTC, which is
// correct only for a business actually operating there.
func WithLocation(loc *time.Location) Option {
	return func(c *Client) {
		if loc != nil {
			c.location = loc
		}
	}
}

// WithCurrency labels the prices Altegio returns.
func WithCurrency(currency string) Option {
	return func(c *Client) { c.currency = currency }
}

// NewClient returns a client for one Altegio location.
//
// partnerToken identifies the integration and userToken the business account.
// Business data needs both; the public booking endpoints need only the first.
func NewClient(partnerToken, userToken, companyID string, logger *slog.Logger, opts ...Option) (*Client, error) {
	switch {
	case partnerToken == "":
		return nil, errors.New("altegio: partner token is required")
	case companyID == "":
		return nil, errors.New("altegio: company id is required")
	case logger == nil:
		return nil, errors.New("altegio: logger is required")
	}

	c := &Client{
		httpClient:   &http.Client{Timeout: defaultTimeout},
		baseURL:      defaultBaseURL,
		partnerToken: partnerToken,
		userToken:    userToken,
		companyID:    companyID,
		limiter:      rate.NewLimiter(rate.Limit(requestsPerSecond), requestBurst),
		logger:       logger,
		maxAttempts:  defaultMaxAttempts,
		sleep:        sleepContext,
		location:     time.UTC,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// CompanyID returns the location this client is bound to.
func (c *Client) CompanyID() string { return c.companyID }

// envelope is the wrapper Altegio puts every response in.
type envelope[T any] struct {
	Success bool            `json:"success"`
	Data    T               `json:"data"`
	Meta    json.RawMessage `json:"meta"`
}

// metaMessage is the explanation carried on a failure. On success meta is an
// empty array rather than an object, which is why it is decoded lazily.
type metaMessage struct {
	Message string `json:"message"`
}

// request describes one call.
type request struct {
	method string
	path   string
	body   any

	// repeatable reports whether sending this request twice is harmless. Reads,
	// validation, and mutations that set one exact desired state qualify;
	// creating an appointment does not.
	repeatable bool
}

// call sends req and decodes the response payload into a value of type T.
//
// It is a package function rather than a method because Go methods cannot take
// type parameters, and every endpoint returns a differently shaped payload
// inside the same envelope.
func call[T any](ctx context.Context, c *Client, req request) (T, error) {
	var zero T

	var body []byte
	if req.body != nil {
		encoded, err := json.Marshal(req.body)
		if err != nil {
			return zero, fmt.Errorf("altegio %s: encode request: %w", req.path, err)
		}
		body = encoded
	}

	var lastErr error
	attempts := 1
	if req.repeatable {
		attempts = c.maxAttempts
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		// Waiting here rather than sleeping keeps the call cancellable: a
		// shutdown does not have to wait out a rate-limit delay.
		if err := c.limiter.Wait(ctx); err != nil {
			return zero, fmt.Errorf("altegio %s: %w", req.path, err)
		}

		payload, err := c.attempt(ctx, req, body)
		if err == nil {
			var decoded T
			if err := json.Unmarshal(payload, &decoded); err != nil {
				return zero, fmt.Errorf("altegio %s: decode response: %w", req.path, err)
			}
			return decoded, nil
		}

		lastErr = err
		if !errors.Is(err, booking.ErrUnavailable) || attempt == attempts {
			return zero, err
		}

		wait := backoff(attempt)
		c.logger.WarnContext(ctx, "retrying an altegio request",
			"path", req.path, "attempt", attempt, "wait", wait.String(), "error", err)

		if err := c.sleep(ctx, wait); err != nil {
			return zero, fmt.Errorf("altegio %s: %w", req.path, err)
		}
	}

	return zero, lastErr
}

// attempt performs one HTTP round trip and returns the decoded data payload.
func (c *Client) attempt(ctx context.Context, req request, body []byte) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, c.baseURL+req.path, reader)
	if err != nil {
		return nil, fmt.Errorf("altegio %s: build request: %w", req.path, err)
	}

	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", c.authorization())
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A transport failure on a request that cannot be repeated leaves the
		// outcome genuinely unknown: the request may have been applied.
		if !req.repeatable {
			return nil, fmt.Errorf("altegio %s: %w: %w", req.path, booking.ErrOutcomeUnknown, err)
		}
		return nil, fmt.Errorf("altegio %s: %w: %w", req.path, booking.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("altegio %s: %w: read response: %w", req.path, booking.ErrUnavailable, err)
	}

	// Successful DELETE endpoints legitimately return 204 with no envelope.
	// Represent that as JSON null so the generic decoder can still finish.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null"), nil
	}

	var env envelope[json.RawMessage]
	decodeErr := json.Unmarshal(raw, &env)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (decodeErr == nil && !env.Success) {
		return nil, c.translate(req, resp.StatusCode, env.Meta)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("altegio %s: decode response: %w", req.path, decodeErr)
	}

	return env.Data, nil
}

// authorization builds the credentials header.
//
// Altegio takes both tokens in one header, comma separated. The partner token
// alone reaches the public booking endpoints; business data needs the user
// token as well.
func (c *Client) authorization() string {
	if c.userToken == "" {
		return "Bearer " + c.partnerToken
	}
	return "Bearer " + c.partnerToken + ", User " + c.userToken
}

// translate turns an HTTP failure into an error the application can act on,
// which is the only vocabulary that should cross this package's boundary.
func (c *Client) translate(req request, status int, meta json.RawMessage) error {
	var detail metaMessage
	_ = json.Unmarshal(meta, &detail) // meta is an empty array on success; absent detail is fine

	message := detail.Message
	if message == "" {
		message = http.StatusText(status)
	}

	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		// Repeating this cannot help: the credentials are wrong or lack the
		// permission, and both are deployment problems.
		return fmt.Errorf("altegio %s: %w: credentials rejected: %s", req.path, booking.ErrRejected, message)

	case status == http.StatusNotFound:
		return fmt.Errorf("altegio %s: %w: %s", req.path, booking.ErrNotFound, message)

	case status == http.StatusTooManyRequests:
		return fmt.Errorf("altegio %s: %w: rate limited: %s", req.path, booking.ErrUnavailable, message)

	case status >= http.StatusInternalServerError:
		return fmt.Errorf("altegio %s: %w: %s", req.path, booking.ErrUnavailable, message)

	default:
		return fmt.Errorf("altegio %s: %w: %w: %s",
			req.path, booking.ErrRejected, errRequestRejected, message)
	}
}

// backoff returns how long to wait before retrying, growing exponentially with
// full jitter.
//
// The jitter matters more than the growth. Without it, every caller that failed
// at the same moment retries at the same moment, and the retry itself becomes
// the next outage.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
	if base > 5*time.Second {
		base = 5 * time.Second
	}
	// Jitter needs spread, not unpredictability. Nothing here is a secret, and
	// a cryptographic source would cost more for no benefit.
	return time.Duration(rand.Int64N(int64(base)) + int64(base)/2) //nolint:gosec // not a security decision
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
