package assistant

import (
	"fmt"
	"strings"
)

// MenuCommand is one entry in the menu a messaging app shows beside the text
// box, and the slash command it sends when tapped.
//
// The list lives here rather than in an adapter because it is a description of
// what this assistant can do, which is the same on every channel. Publishing it
// is the adapter's job; knowing what is on it is not.
type MenuCommand struct {
	// Name is the command without its slash. It stays in Latin letters in
	// every language: it is an identifier the app sends back, not something a
	// customer reads, and Telegram accepts nothing else.
	Name string

	// Description is what the customer actually reads, in their language.
	Description string
}

// LocalisedMenu is the command menu written in one language.
type LocalisedMenu struct {
	// LanguageTag names the language, as the tag a provider expects. Empty is
	// the default menu, shown to everyone whose app is set to a language that
	// has no menu of its own.
	LanguageTag string

	Commands []MenuCommand
}

// menuIn returns the menu written in lang.
//
// Deliberately short. A menu long enough to need reading is a menu nobody
// reads, and everything on it can also simply be asked for in words.
//
// Four of the six entries reuse the label their button already carries. A
// customer who taps "Book a visit" in the menu and then sees "Book a visit" on
// a button is meant to understand that these are the same thing.
func menuIn(lang language) []MenuCommand {
	p := speak(lang)
	return []MenuCommand{
		{Name: "start", Description: p.startAgain},
		{Name: "book", Description: p.bookAVisit},
		{Name: "services", Description: p.servicesPrices},
		{Name: "appointments", Description: p.myAppointments},
		{Name: "person", Description: p.talkToAPerson},
		{Name: "help", Description: p.whatICanDo},
	}
}

// Menus returns the command menu in every language this system writes, and the
// default menu for everyone else.
//
// A menu is the only part of this assistant a customer reads before writing a
// word, and until now it was the only part that was always in English: every
// button and every reply beside it followed the customer's language while the
// menu did not. Providers take one menu per language, so this returns one per
// language and the caller publishes each.
//
// The default is English and comes first. It is what a customer whose app is
// set to German sees, and English is the language a visitor who reads neither
// Armenian nor Russian is most likely to have.
func Menus() []LocalisedMenu {
	menus := make([]LocalisedMenu, 0, len(languages)+1)
	menus = append(menus, LocalisedMenu{Commands: menuIn(languageEnglish)})

	for _, lang := range languages {
		menus = append(menus, LocalisedMenu{
			LanguageTag: string(lang),
			Commands:    menuIn(lang),
		})
	}
	return menus
}

// menuIntents says what tapping each menu entry asks for.
//
// The value is written for the model, not for the customer, and never reaches
// them. It is in English because it is an instruction rather than speech: the
// model answers it in whatever language the conversation is already being held
// in, which the prompt requires and the transcript above it establishes.
//
// Entries answered without the model still belong here. A tap that was handled
// directly is still in the transcript the model reads on the next message, and
// it has to read as the request it was rather than as a slash.
var menuIntents = map[string]string{
	"book":         "book an appointment",
	"services":     "see the services and prices",
	"appointments": "see their own appointments",
	"cancel":       "cancel an appointment",
	"person":       "talk to a person",
}

// commandIn returns the slash command a message carries, without its slash and
// without the @botname a group chat appends, or empty when it carries none.
func commandIn(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}

	word := strings.Fields(trimmed)[0]
	word = strings.TrimPrefix(word, "/")
	if at := strings.IndexByte(word, '@'); at >= 0 {
		word = word[:at]
	}
	return strings.ToLower(word)
}

// expandMenuCommand rewrites a tapped menu entry into the request it stands
// for, so the model is asked something rather than handed a slash.
//
// The marker is bracketed and says plainly that it is a tap. A model reading a
// transcript needs to be able to tell what the customer said from what the
// interface did on their behalf, and it must not echo either back.
func expandMenuCommand(text string) (string, bool) {
	intent, ok := menuIntents[commandIn(text)]
	if !ok {
		return text, false
	}
	return fmt.Sprintf("[the customer tapped the menu: %s]", intent), true
}

// greeting is the answer to a customer opening the chat or asking what this is.
//
// It is written here rather than by the model on purpose. It is the first thing
// anybody sees, it is the same every time, and it arrives instantly and works
// when nothing else does, including when the model is unreachable and when no
// model is configured at all.
func (s *Service) greeting(lang language) string {
	name := s.business.Name
	if name == "" {
		name = "us"
	}
	return fmt.Sprintf(speak(lang).welcome, name)
}
