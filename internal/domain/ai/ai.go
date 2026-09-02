// Package ai describes what the application asks of a language model, in terms
// no particular vendor owns.
//
// The model is an interpreter, never an authority. It reads what a customer
// wrote and asks for named tools to be run; it does not decide what is true,
// what is available, or what has been booked. Everything it asks for is
// validated and executed by ordinary code before any of it becomes real.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Errors an AI provider can report.
var (
	// ErrUnavailable means the provider could not be reached or failed in a
	// way that may succeed later.
	ErrUnavailable = errors.New("ai provider unavailable")

	// ErrRejected means the provider refused the request for a reason that
	// repeating it will not fix, such as a bad key or an unknown model.
	ErrRejected = errors.New("ai provider rejected the request")
)

// Role identifies who produced a turn in the conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of conversation sent to the model.
//
// Only what a person could have read is ever represented here: what the
// customer wrote and what the assistant said back. The model's private
// deliberation is not requested, not stored and not replayed.
type Message struct {
	Role Role
	Text string
}

// ToolCall is the model asking for a named capability to be run.
//
// Arguments arrive as raw JSON because they are not trusted yet. They are
// whatever the model produced, and they are validated by the code that owns
// the tool before anything acts on them.
type ToolCall struct {
	// ID ties a call to its result. Providers require the pairing, and a
	// result returned under the wrong id is silently attributed to the wrong
	// question.
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult is what running a tool produced, to be fed back to the model.
type ToolResult struct {
	CallID string

	// Output is what the model gets to read. On failure it carries an
	// explanation the model can act on, rather than an error that stops the
	// conversation: a customer asking for a day the salon is closed should be
	// told so, not met with silence.
	Output string
}

// Tool is a capability the model may ask for.
type Tool struct {
	Name        string
	Description string

	// Parameters is a JSON Schema describing the arguments. It is the model's
	// only guide to what a tool accepts, so a vague schema produces vague
	// calls.
	Parameters json.RawMessage
}

// Turn is one exchange in the tool-calling loop: what the model asked for last
// time, and what running it produced.
type Turn struct {
	Calls   []ToolCall
	Results []ToolResult
}

// Request is one completion.
type Request struct {
	// Instructions is the standing guidance: who the assistant is, what it may
	// claim, and what it must refuse. It is never sourced from customer input.
	Instructions string

	// Messages is the recent conversation, oldest first and already bounded.
	Messages []Message

	// Turns is the tool-calling exchange so far within this single reply.
	Turns []Turn

	Tools     []Tool
	MaxTokens int
}

// Response is what the model produced.
type Response struct {
	// Text is the reply to show the customer. It is empty when the model asked
	// for tools instead of answering.
	Text string

	// ToolCalls is what the model wants run before it can answer.
	ToolCalls []ToolCall

	Usage Usage
}

// WantsTools reports whether the model asked for work before answering.
func (r Response) WantsTools() bool { return len(r.ToolCalls) > 0 }

// Usage is what the completion cost, so spend can be watched per conversation
// rather than discovered on an invoice.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Provider is a language model this application can talk to.
//
// The interface is deliberately small. Anything a specific vendor offers beyond
// this is not something the application should come to depend on.
type Provider interface {
	// Complete returns the model's next response. It is the only method, and
	// it is stateless: the conversation is rebuilt from stored state on every
	// call, so nothing important lives only in a provider's memory.
	Complete(ctx context.Context, req Request) (Response, error)

	// Model names the model in use, for logging and cost attribution.
	Model() string
}

// ArgumentsInto decodes a tool call's arguments into dst.
//
// Unknown fields are refused. The model inventing a plausible-looking argument
// is a real failure mode, and silently ignoring it means acting on a request
// that was not the one made.
func (c ToolCall) ArgumentsInto(dst any) error {
	if len(c.Arguments) == 0 {
		return fmt.Errorf("tool %s: no arguments given", c.Name)
	}

	decoder := json.NewDecoder(bytes.NewReader(c.Arguments))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("tool %s: %w", c.Name, err)
	}
	return nil
}
