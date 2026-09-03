package assistant

import (
	"strings"
	"unicode"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// language is the language fixed phrases are written in.
//
// Only the three this business actually serves are distinguished. Everything
// else falls back to English, which is what a visitor to Yerevan who reads
// neither Armenian nor Russian will be using.
type language string

const (
	languageArmenian language = "hy"
	languageRussian  language = "ru"
	languageEnglish  language = "en"
)

// phrases are the words this system chooses for itself.
//
// Almost nothing belongs here. Service names, specialists and times come from
// the calendar and are shown as they are stored, and everything conversational
// is written by the model in whatever language the customer is using. What is
// left is the handful of fixed labels a button needs and the sentence to fall
// back on when the model cannot be reached at all.
type phrases struct {
	confirmBooking string
	anotherTime    string
	confirmChange  string
	leaveItAlone   string
	talkToAPerson  string
	bookAVisit     string
	myAppointments string
	servicesPrices string
	welcome        string
	apology        string
}

var phrasebook = map[language]phrases{
	languageEnglish: {
		confirmBooking: "Yes, book it",
		anotherTime:    "Another time",
		confirmChange:  "Yes, go ahead",
		leaveItAlone:   "No, leave it",
		talkToAPerson:  "Talk to a person",
		bookAVisit:     "Book a visit",
		myAppointments: "My appointments",
		servicesPrices: "Services and prices",
		welcome: "Hello! I am the booking assistant at %s. " +
			"I can show you the services, find a free time and book it for you. " +
			"What would you like?",
		apology: "Sorry, I could not check that just now. Please try again in a moment, " +
			"or tap the button and a colleague will take over.",
	},
	languageArmenian: {
		confirmBooking: "Այո, ամրագրեք",
		anotherTime:    "Ուրիշ ժամ",
		confirmChange:  "Այո, հաստատում եմ",
		leaveItAlone:   "Ոչ, թողեք",
		talkToAPerson:  "Կապվել աշխատակցի հետ",
		bookAVisit:     "Ամրագրել այց",
		myAppointments: "Իմ այցերը",
		servicesPrices: "Ծառայություններ և գներ",
		welcome: "Բարև Ձեզ: Ես %s-ի ամրագրման օգնականն եմ: " +
			"Կարող եմ ցույց տալ ծառայությունները, գտնել ազատ ժամ և ամրագրել Ձեզ համար: " +
			"Ինչո՞վ կարող եմ օգնել:",
		apology: "Ներողություն, հիմա չկարողացա ստուգել: Խնդրում եմ փորձեք մի փոքր ուշ, " +
			"կամ սեղմեք կոճակը՝ աշխատակցի հետ կապվելու համար:",
	},
	languageRussian: {
		confirmBooking: "Да, запишите",
		anotherTime:    "Другое время",
		confirmChange:  "Да, подтверждаю",
		leaveItAlone:   "Нет, оставьте",
		talkToAPerson:  "Связаться с сотрудником",
		bookAVisit:     "Записаться",
		myAppointments: "Мои записи",
		servicesPrices: "Услуги и цены",
		welcome: "Здравствуйте! Я помощник по записи в %s. " +
			"Могу показать услуги, найти свободное время и записать вас. " +
			"Чем могу помочь?",
		apology: "Извините, сейчас не получилось проверить. Попробуйте, пожалуйста, чуть позже " +
			"или нажмите кнопку, и с вами свяжется сотрудник.",
	},
}

// speak returns the fixed phrases for lang, falling back to English.
func speak(lang language) phrases {
	if p, ok := phrasebook[lang]; ok {
		return p
	}
	return phrasebook[languageEnglish]
}

// scriptLanguage names the language text is written in, or empty when its
// script does not say.
//
// Script is the only signal worth trusting here. Armenian and Russian each have
// an alphabet of their own, so a single letter settles it, and no word list can
// be wrong the way a word list always eventually is. Latin script says nothing:
// it carries English, and it carries the transliterated Armenian half of Yerevan
// types in.
func scriptLanguage(text string) language {
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Armenian, r):
			return languageArmenian
		case unicode.Is(unicode.Cyrillic, r):
			return languageRussian
		}
	}
	return ""
}

// conversationLanguage picks the language to write fixed phrases in.
//
// The transcript is read newest first, because a customer who switched language
// three messages ago has switched. What the assistant itself last said counts
// as evidence too: the model writes in the customer's language, so its own
// reply resolves the case the script of the customer's message cannot, where an
// Armenian speaker types Armenian in Latin letters.
//
// Only if nothing in the conversation says anything does the language the
// customer set their messaging app to decide, which is all there is to go on
// before they have written a word.
func conversationLanguage(history []conversation.Message, appLanguageTag string) language {
	for i := len(history) - 1; i >= 0; i-- {
		if lang := scriptLanguage(history[i].Text); lang != "" {
			return lang
		}
	}

	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(appLanguageTag)), "-")
	switch language(base) {
	case languageArmenian:
		return languageArmenian
	case languageRussian:
		return languageRussian
	default:
		return languageEnglish
	}
}

// choicesOf turns labels into offered options.
func choicesOf(labels ...string) []messaging.Choice {
	choices := make([]messaging.Choice, 0, len(labels))
	for _, label := range labels {
		choices = append(choices, messaging.Choice{Label: label})
	}
	return choices
}

// confirmBookingChoices are the answers to "shall I book this?".
//
// There is no third button offering to abandon the booking. A customer who has
// changed their mind says so, and a button inviting them to is a button some of
// them will press.
func confirmBookingChoices(lang language) []messaging.Choice {
	p := speak(lang)
	return choicesOf(p.confirmBooking, p.anotherTime)
}

// confirmChangeChoices are the answers to "shall I cancel or move this?".
func confirmChangeChoices(lang language) []messaging.Choice {
	p := speak(lang)
	return choicesOf(p.confirmChange, p.leaveItAlone)
}

// helpChoices offer the only thing worth offering when the assistant itself has
// failed: somebody who has not.
func helpChoices(lang language) []messaging.Choice {
	return choicesOf(speak(lang).talkToAPerson)
}

// menuChoices are what a customer is shown when they open the chat.
func menuChoices(lang language) []messaging.Choice {
	p := speak(lang)
	return choicesOf(p.bookAVisit, p.servicesPrices, p.myAppointments, p.talkToAPerson)
}
