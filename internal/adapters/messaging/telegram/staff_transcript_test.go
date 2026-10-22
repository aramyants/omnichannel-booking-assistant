package telegram

import (
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

func TestHandoffTranscriptDoesNotAttributeNativeStaffReplyToAssistant(t *testing.T) {
	notice := formatHandoff(assistant.HandoffNotice{
		Reason: assistant.ReasonStaffReplied,
		Recent: []conversation.Message{
			{Direction: conversation.DirectionInbound, ExternalMessageID: "customer-1", Text: "Can I change the time?"},
			{Direction: conversation.DirectionOutbound, Text: "I will check availability."},
			{Direction: conversation.DirectionOutbound, ExternalMessageID: "staff-1", Text: "I have changed your appointment."},
		},
	})
	for _, line := range []string{
		"Customer: Can I change the time?",
		"Assistant: I will check availability.",
		"Administrator: I have changed your appointment.",
	} {
		if !strings.Contains(notice, line) {
			t.Errorf("handoff is missing %q: %s", line, notice)
		}
	}
	if strings.Contains(notice, "Assistant: I have changed your appointment.") {
		t.Fatalf("native staff reply attributed to the assistant: %s", notice)
	}
}
