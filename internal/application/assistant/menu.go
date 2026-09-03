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
	Name        string
	Description string
}

// Menu is what a customer is offered.
//
// Deliberately short. A menu long enough to need reading is a menu nobody
// reads, and everything on it can also simply be asked for in words.
func Menu() []MenuCommand {
	return []MenuCommand{
		{Name: "start", Description: "Start again"},
		{Name: "book", Description: "Book a visit"},
		{Name: "services", Description: "Services and prices"},
		{Name: "appointments", Description: "My appointments"},
		{Name: "person", Description: "Talk to a person"},
		{Name: "help", Description: "What I can do"},
	}
}

// menuIntents says what tapping each menu entry asks for.
//
// The value is written for the model, not for the customer, and never reaches
// them. It is in English because it is an instruction rather than speech: the
// model answers it in whatever language the conversation is already being held
// in, which the prompt requires and the transcript above it establishes.
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
