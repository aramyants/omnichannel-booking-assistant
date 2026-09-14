package assistant

import (
	"slices"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestContactQuestionsDoNotInheritCalendarButtons(t *testing.T) {
	for _, question := range []string{
		"13:30 is available. What is your phone number?",
		"Спасибо. На какое имя оформить запись?",
		"Какую фамилию указать вместе с именем Garik?",
	} {
		t.Run(question, func(t *testing.T) {
			sender := &fakeSender{}
			model := &scriptedAI{responses: []ai.Response{
				toolResponse("lookup", toolAvailableSlots, `{"staff_id":"501","date":"`+bookingDay()+`"}`),
				{Text: question, Choices: []string{}},
			}}
			svc, _ := newAIService(t, model, defaultScheduling(), sender)
			if err := svc.Handle(t.Context(), incoming("phone")); err != nil {
				t.Fatal(err)
			}
			if got := sender.sent[0].Choices; len(got) != 0 {
				t.Fatalf("contact question inherited calendar choices: %v", got)
			}
			if !model.requests[0].StructuredReply {
				t.Fatal("reply text and choices were not requested together")
			}
		})
	}
}

func TestChoicesAreRelevantValidatedAndCompact(t *testing.T) {
	sess := &session{}
	sess.offer("Motion sport", "Face motion")
	sess.offer("10:00", "10:30", "11:00", "11:30")
	sess.selectChoices([]string{"invented", "10:30", "10:30", "11:00", "11:30", "10:00"})
	if got, want := labelsOf(sess.buttons("Would 10:30, 11:00 or 11:30 suit?")), []string{"10:30", "11:00", "11:30"}; !slices.Equal(got, want) {
		t.Fatalf("choices = %v, want %v", got, want)
	}
	// A second lookup does not force the reply to ask about that lookup.
	sess.selectChoices([]string{"Face motion", "Motion sport"})
	if got := labelsOf(sess.buttons("Face motion or Motion sport: which service?")); !slices.Equal(got, []string{"Face motion", "Motion sport"}) {
		t.Fatalf("lost earlier tool candidates: %v", got)
	}
	sess.offer()
	sess.selectChoices([]string{"10:30"})
	if got := sess.buttons("Booked."); len(got) != 0 {
		t.Fatalf("completed booking retained choices: %v", got)
	}
}

func TestModelCannotAttachStaleCalendarButtonsToNameQuestion(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("slots", toolAvailableSlots, `{"staff_id":"501","date":"`+bookingDay()+`"}`),
		{Text: "10:30 is available. What name should I book this under?", Choices: []string{"10:00", "10:30", "11:00"}},
	}}
	svc, _ := newAIService(t, model, defaultScheduling(), sender)
	if err := svc.Handle(t.Context(), incoming("stale-calendar-buttons")); err != nil {
		t.Fatal(err)
	}
	if got := sender.sent[0].Choices; len(got) != 0 {
		t.Fatalf("name question showed unrelated time buttons: %v", got)
	}
}

func TestChoiceMustBeNamedAsAWholeLabel(t *testing.T) {
	services := []messaging.Choice{{Label: "Face Motion"}, {Label: "Face Motion Guasha"}}
	if choiceNamedInReply("Face Motion Guasha is available.", "Face Motion", services) {
		t.Fatal("a longer service name was mistaken for a separate option")
	}
	if choiceNamedInReply("It starts at 110:00.", "10:00", nil) {
		t.Fatal("a time embedded in another value was mistaken for an option")
	}
	if !choiceNamedInReply("Would Face Motion or 10:30 work?", "Face Motion", services) ||
		!choiceNamedInReply("Would Face Motion or 10:30 work?", "10:30", nil) {
		t.Fatal("visible choices were lost")
	}
}

func TestContactQuestionsSuppressOldChoicesInEveryCustomerLanguage(t *testing.T) {
	for _, reply := range []string{
		"13:30 is available. What is your phone number?",
		"13:30 свободно. На какое имя оформить запись?",
		"13:30 ազատ է։ Խնդրում եմ գրեք ձեր անունը։",
	} {
		sess := &session{}
		sess.offer("13:30")
		sess.selectChoices([]string{"13:30"})
		if got := sess.buttons(reply); len(got) != 0 {
			t.Errorf("%q inherited calendar buttons: %v", reply, got)
		}
	}
}

func TestConfirmationChoicesCannotBeOverriddenByModel(t *testing.T) {
	sess := &session{language: languageEnglish}
	sess.offer("10:00", "10:30")
	sess.offerFixed(offerBookingConfirmation)
	sess.selectChoices([]string{"10:00"})
	if got := labelsOf(sess.buttons("Shall I book this?")); !slices.Equal(got, labelsOf(confirmBookingChoices(languageEnglish))) {
		t.Fatalf("model replaced confirmation choices: %v", got)
	}
}
