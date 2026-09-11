package firestore

import (
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestInteractionMetadataSurvivesSerialization(t *testing.T) {
	conv := conversation.Conversation{ID: "conv", Provider: messaging.ProviderTelegram, LastChoiceMessageID: "123", State: conversation.StateHumanRequested, HandoffAt: testNow}
	got := fromConversationDoc(toConversationDoc(conv))
	if got.LastChoiceMessageID != "123" || !got.HandoffAt.Equal(testNow) || got.WaitingForHumanLongerThan(time.Hour, testNow.Add(time.Minute)) {
		t.Fatalf("lost durable interaction metadata: %+v", got)
	}
}
