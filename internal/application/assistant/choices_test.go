package assistant

import (
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func incomingText(messageID, text string) messaging.Envelope {
	msg := incoming(messageID)
	msg.Content.Text = text
	return msg
}

func labelsOf(choices []messaging.Choice) []string {
	labels := make([]string, 0, len(choices))
	for _, choice := range choices {
		labels = append(labels, choice.Label)
	}
	return labels
}

// TestFreeTimesAreOfferedAsButtons: a time is the one thing in this exchange
// that is genuinely easier to tap than to type, and the labels are the calendar
// times themselves, so tapping one is the same as typing it.
func TestFreeTimesAreOfferedAsButtons(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolAvailableSlots,
			`{"staff_id":"501","date":"`+bookingDay()+`"}`),
		textResponse("Ten or half past ten suit?"),
	}}
	svc, _ := newAIService(t, model, defaultScheduling(), sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(sender.sent))
	}

	got := labelsOf(sender.sent[0].Choices)
	want := []string{"10:00", "10:30"}
	if len(got) != len(want) {
		t.Fatalf("choices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("choice %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAPreparedBookingOffersTwoAnswers: the only question in the exchange with
// exactly two answers, and the only buttons whose words this system chooses. It
// writes them in whatever language the reply itself was written in.
func TestAPreparedBookingOffersTwoAnswers(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Ամրագրե՞մ այս ժամը:"),
	}}
	svc, _ := newAIService(t, model, defaultScheduling(), sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	got := labelsOf(sender.sent[0].Choices)
	want := []string{
		speak(languageArmenian).confirmBooking,
		speak(languageArmenian).anotherTime,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("choices = %v, want %v", got, want)
	}
}

// TestAConfirmedBookingOffersNothing: nothing left to ask, so nothing left to
// tap. Buttons that outlive their question are how a customer ends up tapping
// a time that was free last week.
func TestAConfirmedBookingOffersNothing(t *testing.T) {
	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{prepareCall("call_1"), textResponse("Shall I book it?")}}
	svc, _ := newAIService(t, model, defaultScheduling(), sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	model.responses = []ai.Response{
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("Booked. Your reference is 998877."),
	}
	model.calls = 0

	if err := svc.Handle(t.Context(), incomingText("4128", "yes please")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if choices := sender.sent[1].Choices; len(choices) != 0 {
		t.Errorf("choices = %v, want none once the appointment exists", labelsOf(choices))
	}
}

// TestOpeningTheChatIsAnsweredWithoutAModel: the first thing anybody sees is
// the one thing that must never be slow, wrong or missing. It is written here
// and shipped with the code, so it arrives instantly and still works on a day
// when the model provider does not.
func TestOpeningTheChatIsAnsweredWithoutAModel(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newAIService(t, nil, defaultScheduling(), sender)

	if err := svc.Handle(t.Context(), incomingText("4127", "/start")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(sender.sent))
	}

	// The customer's app is set to Armenian and they have written nothing else.
	if want := svc.greeting(languageArmenian); sender.sent[0].Text != want {
		t.Errorf("reply = %q, want %q", sender.sent[0].Text, want)
	}
	if !strings.Contains(sender.sent[0].Text, "Studio Nine") {
		t.Errorf("reply = %q, want it to name the business", sender.sent[0].Text)
	}

	if got, want := len(sender.sent[0].Choices), len(menuChoices(languageArmenian)); got != want {
		t.Errorf("offered %d choices, want the whole menu of %d", got, want)
	}
}

// TestConversationLanguageFollowsTheCustomer. Script is the only signal worth
// trusting: one Armenian or Cyrillic letter settles it, and Latin script says
// nothing at all, because it carries English and transliterated Armenian alike.
func TestConversationLanguageFollowsTheCustomer(t *testing.T) {
	said := func(texts ...string) []conversation.Message {
		messages := make([]conversation.Message, 0, len(texts))
		for _, text := range texts {
			messages = append(messages, conversation.Message{Text: text})
		}
		return messages
	}

	tests := map[string]struct {
		history []conversation.Message
		tag     string
		want    language
	}{
		"armenian script wins":       {history: said("Barev", "Բարև Ձեզ"), tag: "en", want: languageArmenian},
		"cyrillic script wins":       {history: said("Здравствуйте"), tag: "en", want: languageRussian},
		"the newest message decides": {history: said("Բարև", "Здравствуйте"), tag: "hy", want: languageRussian},
		"latin says nothing":         {history: said("Barev, inch ka"), tag: "hy", want: languageArmenian},
		"the app decides otherwise":  {history: said("hello"), tag: "ru-RU", want: languageRussian},
		"and english is the floor":   {history: nil, tag: "", want: languageEnglish},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := conversationLanguage(tc.history, tc.tag); got != tc.want {
				t.Errorf("conversationLanguage() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMenuCommandsBecomeRequests: nobody types "/appointments" at a
// receptionist, so the model is shown the request the tap stands for.
func TestMenuCommandsBecomeRequests(t *testing.T) {
	for _, command := range []string{"/book", "/appointments@EmotionBot", "/person"} {
		expanded, ok := expandMenuCommand(command)
		if !ok {
			t.Errorf("expandMenuCommand(%q) did not recognise a menu entry", command)
			continue
		}
		if !strings.HasPrefix(expanded, "[the customer tapped the menu:") {
			t.Errorf("expandMenuCommand(%q) = %q", command, expanded)
		}
	}

	// Anything else is left exactly as the customer wrote it.
	for _, text := range []string{"I would like to book", "/nonsense", "16:30"} {
		if expanded, ok := expandMenuCommand(text); ok || expanded != text {
			t.Errorf("expandMenuCommand(%q) = %q, %v, want it left alone", text, expanded, ok)
		}
	}
}

// TestTheCommandMenuIsWrittenInEveryLanguage. The menu is the only part of this
// assistant a customer reads before writing a word, and it used to be the only
// part that was always in English while every button and reply beside it
// followed them.
func TestTheCommandMenuIsWrittenInEveryLanguage(t *testing.T) {
	menus := Menus()

	// The default menu plus one per language. The default is what a customer
	// whose app is set to German gets, and there has to be one.
	if want := len(languages) + 1; len(menus) != want {
		t.Fatalf("published %d menus, want %d", len(menus), want)
	}
	if menus[0].LanguageTag != "" {
		t.Errorf("first menu is scoped to %q, want the default menu first", menus[0].LanguageTag)
	}

	byTag := map[string][]MenuCommand{}
	for _, menu := range menus {
		if len(menu.Commands) != len(menuIn(languageEnglish)) {
			t.Errorf("the %q menu has %d entries, want every menu to offer the same things",
				menu.LanguageTag, len(menu.Commands))
		}
		for _, command := range menu.Commands {
			// The name is an identifier the app sends back, not something a
			// customer reads. Telegram accepts nothing but lowercase Latin.
			for _, r := range command.Name {
				if (r < 'a' || r > 'z') && r != '_' {
					t.Errorf("command %q in the %q menu is not a usable command name",
						command.Name, menu.LanguageTag)
					break
				}
			}
			if strings.TrimSpace(command.Description) == "" {
				t.Errorf("command %q in the %q menu has no description",
					command.Name, menu.LanguageTag)
			}
		}
		byTag[menu.LanguageTag] = menu.Commands
	}

	// Every language this system writes has a menu of its own, and it is
	// actually translated rather than the English one under another tag.
	for _, lang := range languages {
		commands, ok := byTag[string(lang)]
		if !ok {
			t.Errorf("no menu published for %q", lang)
			continue
		}
		if lang == languageEnglish {
			continue
		}
		for i, command := range commands {
			if command.Description == byTag[""][i].Description {
				t.Errorf("the %q menu still reads %q for /%s",
					lang, command.Description, command.Name)
			}
		}
	}
}

// TestTheMenuAndItsButtonsAgree: a customer who taps "Book a visit" in the menu
// and then sees "Book a visit" on a button is meant to understand that these
// are the same thing, so the two are built from one set of words.
func TestTheMenuAndItsButtonsAgree(t *testing.T) {
	for _, lang := range languages {
		labels := map[string]bool{}
		for _, choice := range menuChoices(lang) {
			labels[choice.Label] = true
		}

		for _, command := range menuIn(lang) {
			switch command.Name {
			case "start", "help":
				// These two have no button of their own.
				continue
			}
			if !labels[command.Description] {
				t.Errorf("the %q menu offers /%s as %q, which no button says",
					lang, command.Name, command.Description)
			}
		}
	}
}

// TestAskingForAPersonNeedsNoModel. Tapping "Talk to a person" is the one
// request this system never needs a model to understand, and routing it through
// one only adds a way for it to be missed. It works here with no model
// configured at all.
func TestAskingForAPersonNeedsNoModel(t *testing.T) {
	sender := &fakeSender{}
	staff := &recordingStaff{}
	svc, store := newAIServiceWithStaff(t, nil, defaultScheduling(), sender, staff)

	if err := svc.Handle(t.Context(), incomingText("4130", "/person")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	// The state change is what actually stops the assistant replying. Anything
	// less is a promise the next message would break.
	if conv := openConversation(t, store); conv.State != conversation.StateHumanRequested {
		t.Errorf("state = %q, want the conversation waiting for a person", conv.State)
	}

	// A colleague has to learn the customer is waiting, or the customer waits
	// for somebody who was never told.
	if len(staff.notices) != 1 {
		t.Fatalf("sent %d handoff notices, want 1", len(staff.notices))
	}
	if staff.notices[0].Reason != ReasonCustomerAsked {
		t.Errorf("reason = %q, want %q", staff.notices[0].Reason, ReasonCustomerAsked)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d replies, want 1", len(sender.sent))
	}
	if want := speak(languageArmenian).handedOver; sender.sent[0].Text != want {
		t.Errorf("reply = %q, want %q", sender.sent[0].Text, want)
	}

	// Somebody waiting for a person is waiting, not choosing.
	if choices := sender.sent[0].Choices; len(choices) != 0 {
		t.Errorf("choices = %v, want none while they wait", labelsOf(choices))
	}
}
