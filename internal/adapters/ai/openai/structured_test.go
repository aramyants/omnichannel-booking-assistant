package openai

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
)

func structuredEnvelope(t *testing.T, status, output string) string {
	t.Helper()
	raw, err := json.Marshal(responsesResponse{
		Status: status,
		Output: []outputItem{{Type: "message", Content: []outputContent{{Type: "output_text", Text: output}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStructuredReplyParsesTextAndChoicesInOneRequest(t *testing.T) {
	srv, sent, _ := serve(t, http.StatusOK, structuredEnvelope(t, "completed", `{"text":"10:00 or 10:30?","choices":["10:00","10:30"]}`))
	resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{StructuredReply: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "10:00 or 10:30?" || !slices.Equal(resp.Choices, []string{"10:00", "10:30"}) {
		t.Fatalf("unexpected customer reply: %+v", resp)
	}
	if sent.Text == nil || sent.Text.Format.Type != "json_schema" || !sent.Text.Format.Strict {
		t.Fatalf("missing strict text.format: %+v", sent.Text)
	}
}

func TestBrokenStructuredRepliesNeverLeakJSONToCustomer(t *testing.T) {
	for name, output := range map[string]string{
		"truncated":    `{"text":"hello`,
		"empty":        `{"text":"  ","choices":[]}`,
		"unstructured": "Hello",
		"refusal":      "",
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, _ := serve(t, http.StatusOK, structuredEnvelope(t, "completed", output))
			resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{StructuredReply: true})
			if err == nil || resp.Text != "" {
				t.Fatalf("unsafe reply: %+v, %v", resp, err)
			}
		})
	}
}

func TestIncompleteResponsesAreNotDelivered(t *testing.T) {
	srv, _, _ := serve(t, http.StatusOK, structuredEnvelope(t, "incomplete", `{"text":"Booked","choices":[]}`))
	if _, err := testClient(t, srv).Complete(t.Context(), ai.Request{StructuredReply: true}); err == nil {
		t.Fatal("accepted unfinished response")
	}
}

func TestStructuredReplyStillAllowsToolCalls(t *testing.T) {
	srv, _, _ := serve(t, http.StatusOK, `{"status":"completed","output":[{"type":"function_call","call_id":"1","name":"list_services","arguments":"{}"}]}`)
	resp, err := testClient(t, srv).Complete(t.Context(), ai.Request{StructuredReply: true})
	if err != nil || !resp.WantsTools() {
		t.Fatalf("tool call rejected: %+v, %v", resp, err)
	}
}
