package openai

import (
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatelessToolLoopPreservesReasoningAndPhase(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Model           string
			Store           bool
			Reasoning       reasoningConfig
			MaxOutputTokens int `json:"max_output_tokens"`
			Input           []json.RawMessage
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Store || req.Reasoning.Effort != "high" || req.MaxOutputTokens != 8192 {
			t.Error("explicit reasoning settings or stateless privacy changed")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-test"},{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"internal progress"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"}]}`))
			return
		}
		var reasoning, phase, function, result map[string]any
		if len(req.Input) != 5 {
			t.Error("missing continuation or duplicated function call")
			return
		}
		_ = json.Unmarshal(req.Input[1], &reasoning)
		_ = json.Unmarshal(req.Input[2], &phase)
		_ = json.Unmarshal(req.Input[3], &function)
		_ = json.Unmarshal(req.Input[4], &result)
		if reasoning["encrypted_content"] != "opaque-test" || phase["phase"] != "commentary" || function["call_id"] != "call_1" || result["type"] != "function_call_output" {
			t.Error("reasoning and tool response ordering lost")
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Your answer."}]}]}`))
	}))
	defer srv.Close()
	client, err := NewClient(testAPIKey, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	req := ai.Request{Messages: []ai.Message{{Role: ai.RoleUser, Text: "question"}}}
	first, err := client.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "" || !first.WantsTools() {
		t.Fatal("commentary must not be a customer answer")
	}
	req.Turns = []ai.Turn{{Calls: first.ToolCalls, Continuation: first.Continuation, Results: []ai.ToolResult{{CallID: "call_1", Output: "safe result"}}}}
	final, err := client.Complete(t.Context(), req)
	if err != nil || final.Text != "Your answer." {
		t.Fatal("stateless reasoning tool loop failed")
	}
}
