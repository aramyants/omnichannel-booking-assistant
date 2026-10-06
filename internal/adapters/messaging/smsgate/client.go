// Package smsgate calls the Android SMSGate API. It does not retry sends:
// after a timeout the caller must reconcile the stable message ID first.
package smsgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

var (
	ErrRejected = errors.New("smsgate rejected request before acceptance")
	// ErrUncertain means sending again could duplicate an accepted SMS.
	ErrUncertain   = errors.New("smsgate acceptance is uncertain; reconcile message ID")
	ErrUnavailable = errors.New("smsgate status unavailable")
	ErrNotFound    = errors.New("smsgate message not found")
	messageID      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
)

const maxResponseBytes = 64 << 10

type Config struct {
	// APIBaseURL includes /3rdparty/v1, or /api/3rdparty/v1 for private mode.
	// Empty selects the public service. No Android LAN port is exposed.
	APIBaseURL string
	Username   string
	Password   string
	DeviceID   string
	SIMNumber  int
}

type Client struct {
	config Config
	http   *http.Client
}

// NewClient requires a registered device and an explicit SIM selection. The
// sender's phone number is determined by that SIM, never by a payload field.
func NewClient(config Config, transport *http.Client) (*Client, error) {
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://api.sms-gate.app/3rdparty/v1"
	}
	config.APIBaseURL = strings.TrimRight(config.APIBaseURL, "/")
	u, err := url.Parse(config.APIBaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasSuffix(u.Path, "/3rdparty/v1") {
		return nil, errors.New("smsgate: invalid API base URL")
	}
	loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return nil, errors.New("smsgate: HTTPS is required")
	}
	if strings.TrimSpace(config.Username) == "" || strings.Contains(config.Username, ":") ||
		config.Password == "" || strings.TrimSpace(config.DeviceID) == "" ||
		config.SIMNumber < 1 || config.SIMNumber > 3 {
		return nil, errors.New("smsgate: username, password, device and SIM (1-3) are required")
	}
	h := http.Client{Timeout: 10 * time.Second}
	if transport != nil {
		h = *transport
		if h.Timeout == 0 {
			h.Timeout = 10 * time.Second
		}
	}
	// Redirects must never leak credentials or re-submit a send elsewhere.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, http: &h}, nil
}

// Message is one transactional SMS for one recipient. The caller persists ID
// before Send, and keeps it unchanged when reconciling an uncertain response.
type Message struct {
	ID         string
	Phone      string
	Text       string
	ValidUntil time.Time
	Priority   int // Use 100 for expiring verification codes; 0 for normal notices.
}

type State string

const (
	Pending    State = "Pending"
	Processed  State = "Processed"
	Sent       State = "Sent"
	Delivered  State = "Delivered"
	Failed     State = "Failed"
	Cancelling State = "Cancelling"
	Cancelled  State = "Cancelled"
)

// Status preserves provider states. Sent is SMSC acceptance. For multipart
// SMS, SMSGate can report Delivered after any part, not necessarily all parts.
type Status struct {
	ID    string `json:"id"`
	State State  `json:"state"`
}

func (s Status) validFor(id string) bool {
	if s.ID != id {
		return false
	}
	switch s.State {
	case Pending, Processed, Sent, Delivered, Failed, Cancelling, Cancelled:
		return true
	}
	return false
}

func (c *Client) Send(ctx context.Context, msg Message) (Status, error) {
	phone, err := customer.NormalizePhone(msg.Phone)
	if !messageID.MatchString(msg.ID) || err != nil || phone != msg.Phone ||
		strings.TrimSpace(msg.Text) == "" || !utf8.ValidString(msg.Text) || utf8.RuneCountInString(msg.Text) > 4096 ||
		!msg.ValidUntil.After(time.Now()) || msg.Priority < -128 || msg.Priority > 127 {
		return Status{}, fmt.Errorf("%w: invalid ID, E.164 recipient, text, expiry or priority", ErrRejected)
	}
	payload := struct {
		ID          string `json:"id"`
		TextMessage struct {
			Text string `json:"text"`
		} `json:"textMessage"`
		PhoneNumbers       []string `json:"phoneNumbers"`
		DeviceID           string   `json:"deviceId"`
		SIMNumber          int      `json:"simNumber"`
		ValidUntil         string   `json:"validUntil"`
		Priority           int      `json:"priority"`
		WithDeliveryReport bool     `json:"withDeliveryReport"`
	}{ID: msg.ID, PhoneNumbers: []string{msg.Phone}, DeviceID: c.config.DeviceID,
		SIMNumber: c.config.SIMNumber, ValidUntil: msg.ValidUntil.UTC().Format(time.RFC3339),
		Priority: msg.Priority, WithDeliveryReport: true}
	payload.TextMessage.Text = msg.Text
	body, err := json.Marshal(payload)
	if err != nil {
		return Status{}, fmt.Errorf("%w: encode request", ErrRejected)
	}
	return c.request(ctx, http.MethodPost, "/messages", msg.ID, body)
}

func (c *Client) Status(ctx context.Context, id string) (Status, error) {
	if !messageID.MatchString(id) {
		return Status{}, fmt.Errorf("%w: invalid message ID", ErrRejected)
	}
	return c.request(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), id, nil)
}

func (c *Client) request(ctx context.Context, method, path, id string, body []byte) (Status, error) {
	failure := ErrUnavailable
	if method == http.MethodPost {
		failure = ErrUncertain
	}
	req, err := http.NewRequestWithContext(ctx, method, c.config.APIBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return Status{}, fmt.Errorf("%w: build request", ErrRejected)
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("%w: transport failed", failure)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if method == http.MethodGet && resp.StatusCode == http.StatusNotFound {
			return Status{}, ErrNotFound
		}
		// Do not echo the response body: it can contain phone numbers or OTPs.
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusUnprocessableEntity:
			failure = ErrRejected
		}
		return Status{}, fmt.Errorf("%w: HTTP %d", failure, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return Status{}, fmt.Errorf("%w: incomplete or oversized response", failure)
	}
	var status Status
	if err := json.Unmarshal(raw, &status); err != nil || !status.validFor(id) {
		return Status{}, fmt.Errorf("%w: invalid status response", failure)
	}
	return status, nil
}
