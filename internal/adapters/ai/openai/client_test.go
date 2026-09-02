package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
)

const testAPIKey = "sk-test-key-value"

// serve answers with body and captures the request that was sent.
func serve(t *testing.T, status int, body string) (*httptest.Server, *responsesRequest, *http.Header) {
	t.Helper()

	var (
		sent    responsesRequest
		headers http.Header
		path    string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(func() {
		srv.Close()
		// The Responses API is used rather than Chat Completions: current
		// models reject a Chat Completions request carrying function tools.
		if path != "" && path != "/responses" {
			t.Errorf("called %q, want /responses", path)
		}
	})

	return srv, &sent, &headers
}

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()

	client, err := NewClient(testAPIKey, WithBaseURL(srv.URL), WithModel("test-model"))
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}
	return client
}

const plainTextResponse = `{
	"id": "resp_1",
	"model": "test-model",
	"status": "completed",
	"output": [
		{"type": "message", "role": "assistant",
		 "content": [{"type": "output_text", "text": "We are open until six."}]}
	],
	"usage": {"input_tokens": 120, "output_tokens": 14}
}`

func TestNewClientRequiresAKey(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Error("NewClient() accepted an empty api key")
	}
}

func TestCompleteReturnsText(t *testing.T) {
	srv, sent, headers := serve(t, http.StatusOK, plainTextResponse)

	resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{
		Instructions: "You are the booking assistant.",
		Messages:     []ai.Message{{Role: ai.RoleUser, Text: "What time do you close?"}},
	})
	if err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	if resp.Text != "We are open until six." {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.WantsTools() {
		t.Error("a plain answer was reported as wanting tools")
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 14 {
		t.Errorf("usage = %+v, want 120 in and 14 out", resp.Usage)
	}

	if got := headers.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", got)
	}

	// Instructions travel outside the input array so they cannot be confused
	// with anything a customer wrote.
	if sent.Instructions != "You are the booking assistant." {
		t.Errorf("instructions = %q", sent.Instructions)
	}
	if len(sent.Input) != 1 || sent.Input[0].Role != "user" {
		t.Errorf("input = %+v, want one user message", sent.Input)
	}
}

// TestCompleteDoesNotLetOpenAIRetainTheConversation matters because this system
// keeps its own transcript and is the only place customer messages belong.
func TestCompleteDoesNotLetOpenAIRetainTheConversation(t *testing.T) {
	srv, sent, _ := serve(t, http.StatusOK, plainTextResponse)

	if _, err := testClient(t, srv).Complete(t.Context(), ai.Request{}); err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}
	if sent.Store {
		t.Error("the request asked OpenAI to store the conversation")
	}
}

// TestToolDefinitionsAreFlat guards the difference that silently breaks tool
// calling: Chat Completions nested name and parameters under a "function" key,
// and the Responses API takes them flat on the tool object.
func TestToolDefinitionsAreFlat(t *testing.T) {
	var raw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_, _ = w.Write([]byte(plainTextResponse))
	}))
	defer srv.Close()

	client, err := NewClient(testAPIKey, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}

	_, err = client.Complete(t.Context(), ai.Request{
		Tools: []ai.Tool{{
			Name:        "list_services",
			Description: "List the services.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one entry", raw["tools"])
	}
	tool, _ := tools[0].(map[string]any)

	if _, nested := tool["function"]; nested {
		t.Error("the tool is nested under a function key, which the Responses API rejects")
	}
	if tool["name"] != "list_services" {
		t.Errorf("name = %v, want it flat on the tool object", tool["name"])
	}
	if tool["type"] != "function" {
		t.Errorf("type = %v, want function", tool["type"])
	}
	// Strict mode is what makes the model's arguments conform to the schema
	// rather than approximate it.
	if tool["strict"] != true {
		t.Error("strict is not set, so arguments may not match the schema")
	}
}

func TestCompleteReturnsToolCalls(t *testing.T) {
	srv, _, _ := serve(t, http.StatusOK, `{
		"id": "resp_2",
		"output": [
			{"type": "reasoning", "id": "rs_1"},
			{"type": "function_call", "id": "fc_1", "call_id": "call_abc",
			 "name": "find_available_slots",
			 "arguments": "{\"staff_id\":\"501\",\"date\":\"2026-09-04\"}"}
		],
		"usage": {"input_tokens": 200, "output_tokens": 30}
	}`)

	resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{})
	if err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	if !resp.WantsTools() {
		t.Fatal("a tool call was not reported")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("returned %d tool calls, want 1", len(resp.ToolCalls))
	}

	call := resp.ToolCalls[0]
	if call.ID != "call_abc" {
		t.Errorf("id = %q, want the call_id not the item id", call.ID)
	}
	if call.Name != "find_available_slots" {
		t.Errorf("name = %q", call.Name)
	}

	var args struct {
		StaffID string `json:"staff_id"`
		Date    string `json:"date"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		t.Fatalf("ArgumentsInto() returned error: %v", err)
	}
	if args.StaffID != "501" || args.Date != "2026-09-04" {
		t.Errorf("arguments = %+v", args)
	}
}

// TestReasoningIsNeverReturnedAsText guards a rule the product depends on: the
// model's private deliberation is not the assistant's answer.
func TestReasoningIsNeverReturnedAsText(t *testing.T) {
	srv, _, _ := serve(t, http.StatusOK, `{
		"output": [
			{"type": "reasoning", "id": "rs_1",
			 "content": [{"type": "reasoning_text", "text": "the customer probably wants Friday"}]},
			{"type": "message", "role": "assistant",
			 "content": [{"type": "output_text", "text": "Friday works."}]}
		],
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`)

	resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{})
	if err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}
	if resp.Text != "Friday works." {
		t.Errorf("text = %q, want only the assistant message", resp.Text)
	}
	if strings.Contains(resp.Text, "probably") {
		t.Error("the model's reasoning leaked into the reply")
	}
}

// TestToolExchangeIsSentInOrder: a result that appears before the call it
// answers is not matched to it.
func TestToolExchangeIsSentInOrder(t *testing.T) {
	srv, sent, _ := serve(t, http.StatusOK, plainTextResponse)

	_, err := testClient(t, srv).Complete(t.Context(), ai.Request{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "Is Friday free?"}},
		Turns: []ai.Turn{{
			Calls: []ai.ToolCall{{
				ID:        "call_abc",
				Name:      "find_available_slots",
				Arguments: json.RawMessage(`{"staff_id":"501"}`),
			}},
			Results: []ai.ToolResult{{CallID: "call_abc", Output: `{"times":["10:00"]}`}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	if len(sent.Input) != 3 {
		t.Fatalf("sent %d input items, want 3", len(sent.Input))
	}
	if sent.Input[0].Role != "user" {
		t.Errorf("item 0 = %+v, want the customer message first", sent.Input[0])
	}
	if sent.Input[1].Type != "function_call" || sent.Input[1].CallID != "call_abc" {
		t.Errorf("item 1 = %+v, want the function call", sent.Input[1])
	}
	if sent.Input[2].Type != "function_call_output" || sent.Input[2].CallID != "call_abc" {
		t.Errorf("item 2 = %+v, want the matching result", sent.Input[2])
	}
}

func TestErrorTranslation(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   error
	}{
		"rejected key": {
			status: http.StatusUnauthorized,
			body:   `{"error":{"message":"Incorrect API key provided"}}`,
			want:   ai.ErrRejected,
		},
		"unknown model": {
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"The model does not exist"}}`,
			want:   ai.ErrRejected,
		},
		"rate limited": {
			status: http.StatusTooManyRequests,
			body:   `{"error":{"message":"Rate limit reached"}}`,
			want:   ai.ErrUnavailable,
		},
		"outage": {
			status: http.StatusBadGateway,
			body:   `{"error":{"message":"Bad gateway"}}`,
			want:   ai.ErrUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv, _, _ := serve(t, tt.status, tt.body)

			_, err := testClient(t, srv).Complete(t.Context(), ai.Request{})
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestTransportFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close()

	client, err := NewClient(testAPIKey, WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}

	if _, err := client.Complete(t.Context(), ai.Request{}); !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("error = %v, want ErrUnavailable", err)
	}
}

func TestArgumentsIntoRefusesInventedFields(t *testing.T) {
	call := ai.ToolCall{
		Name:      "find_available_slots",
		Arguments: json.RawMessage(`{"staff_id":"501","urgency":"high"}`),
	}

	var args struct {
		StaffID string `json:"staff_id"`
	}
	// Silently ignoring an invented argument means acting on a request that was
	// not the one the model made.
	if err := call.ArgumentsInto(&args); err == nil {
		t.Error("ArgumentsInto() accepted an argument that is not in the schema")
	}
}
