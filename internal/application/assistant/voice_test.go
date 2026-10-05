package assistant

import (
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

func TestWithoutHeartsPreservesBookingDetails(t *testing.T) {
	tests := []struct{ input, want string }{
		{"Սիրով ❤️ կսպասենք Ձեզ։\n\nԺամ՝ 14:30\nՀեռախոս՝ +37494768067", "Սիրով կսպասենք Ձեզ։\n\nԺամ՝ 14:30\nՀեռախոս՝ +37494768067"},
		{"❤️‍🔥 Շնորհակալություն ❤️‍🩹", "Շնորհակալություն"},
		{"🩷🩵🩶💙🤍🫶🥰😍😘", ""},
		{"✅ Ձեր ամրագրումը հաստատված է։\n\nԳին՝ 29,000 AMD", "✅ Ձեր ամրագրումը հաստատված է։\n\nԳին՝ 29,000 AMD"},
	}
	for _, tt := range tests {
		if got := withoutHearts(tt.input); got != tt.want {
			t.Errorf("withoutHearts(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHandleStoresExactlyTheHeartFreeReplySent(t *testing.T) {
	model := &scriptedAI{responses: []ai.Response{textResponse("Կստուգենք ❤️ հասանելի ժամերը։")}}
	sender := &fakeSender{}
	svc, store := newAIService(t, model, defaultScheduling(), sender)
	msg := incoming("voice-policy")
	msg.Content.Text = "Բարև, այսօր ազատ ժամ ունե՞ք։"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].Text != "Կստուգենք հասանելի ժամերը։" {
		t.Fatalf("replies = %+v", sender.sent)
	}
	conv := openConversation(t, store)
	history, err := svc.History(t.Context(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Text != msg.Content.Text || history[1].Text != sender.sent[0].Text {
		t.Fatalf("history differs from the actual exchange: %+v", history)
	}
}

func TestHeartOnlyModelReplyOffersRecoveryWithoutMutingAssistant(t *testing.T) {
	model := &scriptedAI{responses: []ai.Response{textResponse("❤️")}}
	sender := &fakeSender{}
	svc, store := newAIService(t, model, defaultScheduling(), sender)
	msg := incoming("empty-heart-reply")
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || strings.TrimSpace(sender.sent[0].Text) == "" {
		t.Fatalf("replies = %+v", sender.sent)
	}
	conv := openConversation(t, store)
	if conv.State != conversation.StateAssistantActive {
		t.Errorf("state = %s, want the assistant to remain available", conv.State)
	}
}
