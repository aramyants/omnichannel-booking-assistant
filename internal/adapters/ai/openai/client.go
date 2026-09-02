// Package openai adapts the OpenAI Responses API to the application's AI port.
// OpenAI request and response shapes do not leave this package.
package openai

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

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"

	// DefaultModel is the model used when none is configured. Model names
	// change faster than deployments, so it is overridable: a wrong one is a
	// configuration fix rather than a release.
	DefaultModel = "gpt-5.6-terra"

	// defaultTimeout is generous because a customer is waiting on the reply,
	// but bounded because they will not wait forever.
	defaultTimeout = 60 * time.Second

	defaultMaxTokens = 1024

	maxResponseBytes = 8 << 20
)

// Client talks to the OpenAI Responses API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
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

// WithModel selects the model.
func WithModel(model string) Option {
	return func(c *Client) {
		if model != "" {
			c.model = model
		}
	}
}

// NewClient returns a client authenticated with an API key.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("openai: api key is required")
	}

	c := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
		model:      DefaultModel,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Model names the model in use.
func (c *Client) Model() string { return c.model }

// Complete returns the model's next response.
//
// The call is stateless. The whole conversation is rebuilt from stored state
// every time and OpenAI is asked not to retain it, so nothing the business
// depends on lives only in a vendor's memory.
func (c *Client) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	payload := responsesRequest{
		Model:           c.model,
		Instructions:    req.Instructions,
		Input:           buildInput(req),
		Tools:           buildTools(req.Tools),
		MaxOutputTokens: maxTokens,
		Store:           false,
	}

	var parsed responsesResponse
	if err := c.post(ctx, "/responses", payload, &parsed); err != nil {
		return ai.Response{}, err
	}

	if parsed.Error != nil {
		return ai.Response{}, fmt.Errorf("openai: %w: %s", ai.ErrRejected, parsed.Error.Message)
	}

	return toResponse(parsed), nil
}

// buildInput flattens the conversation into the shape the Responses API takes.
//
// The order matters: prior conversation first, then the tool exchange from this
// reply, because a tool result that appears before the call it answers is not
// matched to it.
func buildInput(req ai.Request) []inputItem {
	items := make([]inputItem, 0, len(req.Messages)+len(req.Turns)*2)

	for _, msg := range req.Messages {
		items = append(items, inputItem{
			Role:    string(msg.Role),
			Content: msg.Text,
		})
	}

	for _, turn := range req.Turns {
		for _, call := range turn.Calls {
			items = append(items, inputItem{
				Type:      "function_call",
				CallID:    call.ID,
				Name:      call.Name,
				Arguments: string(call.Arguments),
			})
		}
		for _, result := range turn.Results {
			items = append(items, inputItem{
				Type:   "function_call_output",
				CallID: result.CallID,
				Output: result.Output,
			})
		}
	}

	return items
}

func buildTools(tools []ai.Tool) []toolDef {
	if len(tools) == 0 {
		return nil
	}

	defs := make([]toolDef, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, toolDef{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Strict:      true,
		})
	}
	return defs
}

// toResponse extracts the reply text and any tool calls from the output array.
func toResponse(parsed responsesResponse) ai.Response {
	var (
		text  strings.Builder
		calls []ai.ToolCall
	)

	for _, item := range parsed.Output {
		switch item.Type {
		case "function_call":
			calls = append(calls, ai.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: json.RawMessage(item.Arguments),
			})

		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" {
					text.WriteString(content.Text)
				}
			}

		default:
			// Reasoning and other item types are deliberately ignored. The
			// model's private deliberation is not the assistant's answer and is
			// never shown to a customer or written to the transcript.
		}
	}

	return ai.Response{
		Text:      strings.TrimSpace(text.String()),
		ToolCalls: calls,
		Usage: ai.Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
		},
	}
}

func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("openai %s: encode request: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai %s: build request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai %s: %w: %w", path, ai.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("openai %s: %w: read response: %w", path, ai.ErrUnavailable, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.translate(path, resp.StatusCode, raw)
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("openai %s: decode response: %w", path, err)
	}
	return nil
}

// translate turns an HTTP failure into an error the application can act on.
func (c *Client) translate(path string, status int, raw []byte) error {
	var envelope struct {
		Error *apiError `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)

	message := http.StatusText(status)
	if envelope.Error != nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}

	switch {
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		// Worth trying again: the caller decides whether the customer is still
		// waiting.
		return fmt.Errorf("openai %s: %w: %s", path, ai.ErrUnavailable, message)

	default:
		// A rejected key, an unknown model or a malformed request all fail
		// identically every time.
		return fmt.Errorf("openai %s: %w: %s", path, ai.ErrRejected, message)
	}
}
