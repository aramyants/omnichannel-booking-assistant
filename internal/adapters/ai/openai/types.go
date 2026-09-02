package openai

import "encoding/json"

// The types in this file mirror the OpenAI Responses API wire format and are
// unexported: nothing outside this package should depend on it.
//
// The Responses API is used rather than Chat Completions because current models
// reject a Chat Completions request that carries function tools unless
// reasoning is disabled, which would give up the reasoning this assistant
// depends on to interpret a booking request.

type responsesRequest struct {
	Model string `json:"model"`

	// Instructions is the standing guidance, kept out of the input array so it
	// cannot be confused with anything a customer wrote.
	Instructions string `json:"instructions,omitempty"`

	Input []inputItem `json:"input"`
	Tools []toolDef   `json:"tools,omitempty"`

	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	// Store is false so OpenAI does not retain the conversation. This system
	// keeps its own transcript and is the only place customer messages belong.
	Store bool `json:"store"`
}

// toolDef declares a function the model may call.
//
// The Responses API takes name, description and parameters flat on the tool
// object. Chat Completions nested them under a "function" key, and a request
// shaped that way is rejected here.
type toolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`

	// Strict makes the model's arguments conform to the schema rather than
	// approximate it, which is what allows the arguments to be decoded without
	// guessing at missing fields.
	Strict bool `json:"strict"`
}

// inputItem is one entry in the conversation sent to the model. It is either a
// role-carrying message, a function call the model previously made, or the
// result of running one.
type inputItem struct {
	Type string `json:"type,omitempty"`

	// Message fields.
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`

	// Function call fields, echoed back so the model can see what it asked for.
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// Function result field.
	Output string `json:"output,omitempty"`
}

type responsesResponse struct {
	ID     string       `json:"id"`
	Model  string       `json:"model"`
	Status string       `json:"status"`
	Output []outputItem `json:"output"`
	Usage  usage        `json:"usage"`
	Error  *apiError    `json:"error"`
}

type outputItem struct {
	Type string `json:"type"`

	// Message fields.
	Role    string          `json:"role"`
	Content []outputContent `json:"content"`

	// Function call fields.
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type outputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
