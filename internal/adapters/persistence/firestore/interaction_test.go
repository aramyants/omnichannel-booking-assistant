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

func TestCatalogueAndConsentSurviveSerialization(t *testing.T) {
	conv := conversation.Conversation{ID: "conv", Provider: messaging.ProviderWhatsApp, CatalogueCategory: "Motion Sport", CatalogueServiceID: "123", CatalogueStaffID: "456", CataloguePage: 2, ReminderOptIn: true, LastChoiceMessageID: "nonce"}
	got := fromConversationDoc(toConversationDoc(conv))
	if got.CatalogueCategory != conv.CatalogueCategory || got.CatalogueServiceID != conv.CatalogueServiceID || got.CatalogueStaffID != conv.CatalogueStaffID || got.CataloguePage != 2 || !got.ReminderOptIn || got.LastChoiceMessageID != "nonce" {
		t.Fatalf("lost menu/consent: %+v", got)
	}
	conv.ReminderOptIn = false
	conv.CatalogueCategory, conv.CatalogueServiceID, conv.CatalogueStaffID, conv.CataloguePage = "", "", "", 0
	got = fromConversationDoc(toConversationDoc(conv))
	if got.ReminderOptIn || got.CatalogueCategory != "" || got.CatalogueServiceID != "" || got.CatalogueStaffID != "" || got.CataloguePage != 0 {
		t.Fatal("cleared state was restored")
	}
}
