package assistant

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestNavigationAbandonsOnlyUnconfirmedMutations(t *testing.T) {
	svc, _ := newAIService(t, nil, describedCategoryCalendar(), &fakeSender{})
	conv := conversation.Conversation{Draft: &booking.Draft{}, BookingChange: &booking.ChangeDraft{}}
	sess := &session{conv: &conv, language: languageEnglish}
	if _, ok := svc.navigateCatalogue(t.Context(), sess, "/services", false); !ok {
		t.Fatal("navigation not handled")
	}
	if conv.Draft != nil || conv.BookingChange != nil {
		t.Fatal("old confirmation survived a new selection")
	}
}

func TestCoordinatedTreatmentCannotBeBookedThroughTools(t *testing.T) {
	calendar := defaultScheduling()
	calendar.services[0].Category = "Motion Four Hands"
	model := &scriptedAI{responses: []ai.Response{prepareCall("prepare"), textResponse("A colleague will help coordinate this treatment.")}}
	svc, store := newAIService(t, model, calendar, &fakeSender{})
	if err := svc.Handle(t.Context(), incoming("coordination")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultOf(t, model, 1), "two simultaneous therapists") {
		t.Fatal("tool did not explain coordination requirement")
	}
	if openConversation(t, store).Draft != nil || len(calendar.created) != 0 || len(calendar.checked) != 0 {
		t.Fatal("unsafe booking was prepared")
	}
}

func TestWhatsAppOptOutSurvivesHumanHandover(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newAIService(t, nil, defaultScheduling(), sender)
	svc.senders[messaging.ProviderWhatsApp] = sender
	msg := incomingText("hello", "/start")
	msg.Provider = messaging.ProviderWhatsApp
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: msg.Provider, ExternalThreadID: msg.ExternalThreadID})
	if err != nil {
		t.Fatal(err)
	}
	conv.ReminderOptIn = true
	conv.State = conversation.StateHumanActive
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	msg.ExternalMessageID, msg.Content.Text = "opt-out", "STOP"
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	conv, err = store.FindByID(t.Context(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ReminderOptIn || conv.State != conversation.StateHumanActive || len(sender.sent) != 1 {
		t.Fatal("opt-out lost or human handover interrupted")
	}
}

func TestCategoryServiceNavigationAndBack(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newAIService(t, nil, describedCategoryCalendar(), sender)
	n := navigationSpeak(languageArmenian)
	for i, text := range []string{"/start", "1", "1", n.book, n.back, n.back} {
		if err := svc.Handle(t.Context(), incomingText(fmt.Sprint("nav-", i), text)); err != nil {
			t.Fatal(err)
		}
		if i == 2 && !strings.Contains(sender.sent[i].Text, "Դեմքի նուրբ խնամք") {
			t.Fatalf("service with same name as category did not open: %s", sender.sent[i].Text)
		}
		if i == 3 && !strings.Contains(sender.sent[i].Text, n.askDate) {
			t.Fatal("missing date question")
		}
	}
	conv := openConversation(t, store)
	if conv.CatalogueCategory != "" || conv.CatalogueServiceID != "" {
		t.Fatalf("back did not reach root: %+v", conv)
	}
	if len(sender.sent[4].Choices) != 3 {
		t.Fatal("back did not restore category services")
	}
}

func TestCataloguePaginationKeepsAllItemsReachable(t *testing.T) {
	calendar := categoryCalendar()
	calendar.services = nil
	for i := 0; i < 20; i++ {
		calendar.services = append(calendar.services, booking.Service{ID: fmt.Sprint(i), Name: fmt.Sprintf("Treatment %02d", i), Category: "Treatments"})
	}
	sender := &fakeSender{}
	svc, _ := newAIService(t, nil, calendar, sender)
	n := navigationSpeak(languageArmenian)
	sequence := []string{"/services", "1", n.next, n.next, n.next, n.previous}
	for i, text := range sequence {
		if err := svc.Handle(t.Context(), incomingText(fmt.Sprint("page-", i), text)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, reply := range sender.sent[1:5] {
		if len(reply.Choices) > 10 {
			t.Fatal("WhatsApp list limit exceeded")
		}
		for _, choice := range reply.Choices {
			seen[choice.Label] = true
		}
	}
	for _, service := range calendar.services {
		if !seen[service.Name] {
			t.Fatalf("unreachable: %s", service.Name)
		}
	}
	if !strings.Contains(sender.sent[5].Text, "Treatment 12") {
		t.Fatal("previous page failed")
	}
}

func TestMetaStaleChoiceCannotNavigateCurrentPrompt(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newAIService(t, nil, describedCategoryCalendar(), sender)
	svc.senders[messaging.ProviderMessenger] = sender
	msg := incomingText("first", "/services")
	msg.Provider = messaging.ProviderMessenger
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	first := sender.sent[0].ChoiceToken
	msg.ExternalMessageID = "second"
	msg.Content.Text = "Face Motion"
	msg.ChoiceMessageID = first
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if sender.sent[1].ChoiceToken == first || first == "" {
		t.Fatal("choice generation not rotated")
	}
	msg.ExternalMessageID = "third"
	msg.Content.Text = "Motion Sport" // old root button
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if sender.sent[2].Text != expiredChoice(languageArmenian) {
		t.Fatalf("old choice accepted: %s", sender.sent[2].Text)
	}
	_ = store
}

func TestCoordinatedTreatmentsNeverHaveBookButton(t *testing.T) {
	for _, category := range []string{"Motion Four Hands", "Add More Time"} {
		calendar := categoryCalendar()
		calendar.services = []booking.Service{{ID: "special", Name: "Special treatment", Category: category}}
		sender := &fakeSender{}
		svc, _ := newAIService(t, nil, calendar, sender)
		for i, text := range []string{"/services", "1", "1"} {
			if err := svc.Handle(t.Context(), incomingText(fmt.Sprint("special-", i), text)); err != nil {
				t.Fatal(err)
			}
		}
		choices := labelsOf(sender.sent[2].Choices)
		if slices.Contains(choices, navigationSpeak(languageArmenian).book) || !slices.Contains(choices, speak(languageArmenian).talkToAPerson) {
			t.Fatalf("unsafe choices %v", choices)
		}
	}
}
