package assistant

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
)

func describedCategoryCalendar() *stubScheduling {
	calendar := categoryCalendar()
	calendar.services[0].Duration = 60 * time.Minute
	calendar.services[0].Description = "Gentle facial care.\nМягкий уход за лицом.\nԴեմքի նուրբ խնամք։"
	calendar.services[1].Duration = 45 * time.Minute
	calendar.services[1].Description = "Gua sha facial massage.\nМассаж лица гуаша.\nԴեմքի գուաշա մերսում։"
	return calendar
}

func TestBookActionOpensRealCategoriesWithoutWaitingForTheModel(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{}
	svc, store := newAIService(t, model, describedCategoryCalendar(), sender)

	if err := svc.Handle(t.Context(), incomingText("catalogue-1", "/book")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 {
		t.Fatalf("opening the catalogue called the model %d times", model.calls)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want one", len(sender.sent))
	}
	want := []string{"Face Motion", "Motion Sport", "Motion Relax"}
	if got := labelsOf(sender.sent[0].Choices); !slices.Equal(got, want) {
		t.Fatalf("category buttons = %v, want %v", got, want)
	}
	for _, part := range []string{"1. Face Motion", "2. Motion Sport", "3. Motion Relax", "\n\n"} {
		if !strings.Contains(sender.sent[0].Text, part) {
			t.Errorf("catalogue %q does not contain %q", sender.sent[0].Text, part)
		}
	}

	conv := openConversation(t, store)
	if !slices.Equal(conv.PresentedChoices, want) {
		t.Fatalf("stored numbered choices = %v, want %v", conv.PresentedChoices, want)
	}
}

func TestNumberOpensOnlyThatCategoryWithItsLocalizedDescriptions(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{}
	svc, store := newAIService(t, model, describedCategoryCalendar(), sender)

	if err := svc.Handle(t.Context(), incomingText("catalogue-1", "/services")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(t.Context(), incomingText("catalogue-2", "1")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 {
		t.Fatalf("a numbered category needed %d model calls", model.calls)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent %d messages, want two", len(sender.sent))
	}
	reply := sender.sent[1]
	if got, want := labelsOf(reply.Choices), []string{"Face motion", "Face Motion Guasha"}; !slices.Equal(got, want) {
		t.Fatalf("service buttons = %v, want %v", got, want)
	}
	for _, wanted := range []string{"1. Face motion", "2. Face Motion Guasha", "60 րոպե", "29\u202f000–30\u202f000 AMD", "Դեմքի նուրբ խնամք"} {
		if !strings.Contains(reply.Text, wanted) {
			t.Errorf("service list %q does not contain %q", reply.Text, wanted)
		}
	}
	for _, unwanted := range []string{"Motion sport", "Motion Relax", "Gentle facial care", "Мягкий уход"} {
		if strings.Contains(reply.Text, unwanted) {
			t.Errorf("service list contains unrelated or wrong-language copy %q: %s", unwanted, reply.Text)
		}
	}

	conv := openConversation(t, store)
	if got, want := conv.PresentedChoices, []string{"Face motion", "Face Motion Guasha"}; !slices.Equal(got, want) {
		t.Fatalf("current numbered choices = %v, want services %v", got, want)
	}
}

func TestNumberedServiceIsCanonicalInputToTheAssistant(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{textResponse("Which specialist would you prefer?")}}
	svc, _ := newAIService(t, model, describedCategoryCalendar(), sender)

	for i, text := range []string{"/services", "1", "2"} {
		if err := svc.Handle(t.Context(), incomingText("catalogue-"+string(rune('1'+i)), text)); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 1 {
		t.Fatalf("selecting the service called the model %d times, want one", model.calls)
	}
	messages := model.requests[0].Messages
	if len(messages) == 0 || messages[len(messages)-1].Text != "Face Motion Guasha" {
		t.Fatalf("latest model input = %+v, want canonical selected service", messages)
	}
}

func TestVisiblePersonButtonHandsOverWithoutAModel(t *testing.T) {
	sender := &fakeSender{}
	staff := &recordingStaff{}
	svc, _ := newAIServiceWithStaff(t, nil, defaultScheduling(), sender, staff)

	if err := svc.Handle(t.Context(), incomingText("person-label", speak(languageArmenian).talkToAPerson)); err != nil {
		t.Fatal(err)
	}
	if len(staff.notices) != 1 || len(sender.sent) != 1 {
		t.Fatalf("staff notices=%d customer replies=%d, want one each", len(staff.notices), len(sender.sent))
	}
	if sender.sent[0].Text != speak(languageArmenian).handedOver {
		t.Fatalf("handoff reply = %q", sender.sent[0].Text)
	}
}
