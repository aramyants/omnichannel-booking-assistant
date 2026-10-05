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

// languages are every language this system writes, English first.
//
// It exists because a map has no order, and one of the things built from the
// phrasebook is published rather than merely returned: the command menu goes to
// Telegram once per language, and a menu that arrives in a different order on
// every deploy is a menu nobody can diff.
var languages = []language{languageEnglish, languageArmenian, languageRussian}

// phrases are the words this system chooses for itself.
//
// Almost nothing belongs here. Service names, specialists and times come from
// the calendar and are shown as they are stored, and everything conversational
// is written by the model in whatever language the customer is using. What is
// left is the handful of fixed labels a button or a menu entry needs, and the
// sentences to fall back on when the model cannot be reached at all.
type phrases struct {
	confirmBooking string
	anotherTime    string
	confirmChange  string
	leaveItAlone   string
	talkToAPerson  string
	bookAVisit     string
	myAppointments string
	servicesPrices string

	// startAgain and whatICanDo label the two menu entries that have no button
	// of their own. Every other entry reuses the label its button already has,
	// because a customer who taps "Book a visit" in the menu and then sees
	// "Book a visit" on a button should be reading the same words.
	startAgain string
	whatICanDo string

	welcome string
	apology string

	// handedOver is the answer to a customer asking for a person outright.
	// Written here rather than by the model because the request is unambiguous
	// and the answer must not depend on anything that can fail.
	handedOver string

	// noModel and noModelUnsupported are what the assistant says when no model
	// is configured at all. They promise nothing this system cannot do without
	// one, which is to pass the message on.
	noModel            string
	noModelUnsupported string
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
		startAgain:     "Start again",
		whatICanDo:     "How we can help",
		welcome: "Hello. This is the booking assistant at %s.\n\n" +
			"We can help with services, prices and appointments.\n\n" +
			"How can we help?",
		apology: "Sorry, we could not check that just now. Please try again in a moment, " +
			"or tap the button and a colleague will take over.",
		handedOver: "Of course. A colleague will reply here shortly.",
		noModel: "Thank you, we have received your message. " +
			"A colleague will follow up with you shortly.",
		noModelUnsupported: "Thank you. We can only read text messages at the moment, so we could not " +
			"open what you sent. Could you describe what you need in a message?",
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
		startAgain:     "Սկսել նորից",
		whatICanDo:     "Ինչով կարող ենք օգնել",
		welcome: "Բարև Ձեզ։ Սա %s-ի ամրագրման օգնականն է։\n\n" +
			"Կարող ենք ներկայացնել ծառայություններն ու գները և օգնել ամրագրել այցը։\n\n" +
			"Ինչո՞վ կարող ենք օգնել Ձեզ։",
		apology: "Ներողություն, այս պահին չկարողացանք ստուգել։ Խնդրում ենք փորձել մի փոքր ուշ " +
			"կամ սեղմել կոճակը՝ աշխատակցի հետ կապվելու համար։",
		handedOver: "Ձեր հարցը փոխանցել ենք մեր թիմին։ Աշխատակիցը կպատասխանի այստեղ։",
		noModel: "Շնորհակալություն, ստացել ենք Ձեր հաղորդագրությունը։ " +
			"Աշխատակիցը կկապվի Ձեզ հետ։",
		noModelUnsupported: "Շնորհակալություն։ Այս պահին կարող ենք կարդալ միայն տեքստային " +
			"հաղորդագրություններ, ուստի չկարողացանք բացել ուղարկածը։ " +
			"Խնդրում ենք գրել Ձեր հարցը։",
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
		startAgain:     "Начать заново",
		whatICanDo:     "Как мы можем помочь",
		welcome: "Здравствуйте! Это помощник по записи в %s.\n\n" +
			"Поможем с услугами, ценами и записью на визит.\n\n" +
			"Чем можем помочь?",
		apology: "Извините, сейчас не получилось проверить. Попробуйте, пожалуйста, чуть позже " +
			"или нажмите кнопку, и с вами свяжется сотрудник.",
		handedOver: "Конечно. Сотрудник ответит здесь в ближайшее время.",
		noModel: "Спасибо, мы получили ваше сообщение. " +
			"Сотрудник свяжется с вами в ближайшее время.",
		noModelUnsupported: "Спасибо. Сейчас мы можем читать только текстовые сообщения, " +
			"поэтому не смогли открыть то, что вы прислали. " +
			"Опишите, пожалуйста, что вам нужно.",
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

// languageOfTag reads an IETF language tag as one of the languages this system
// writes, falling back to English.
//
// Tags arrive as "ru", "en-GB", "hy-AM"; only the primary subtag names the
// language.
func languageOfTag(tag string) language {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	switch language(base) {
	case languageArmenian:
		return languageArmenian
	case languageRussian:
		return languageRussian
	default:
		return languageEnglish
	}
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
	return languageOfTag(appLanguageTag)
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
	return choicesOf(p.bookAVisit, p.servicesPrices, p.myAppointments, p.talkToAPerson, contactLabel(lang))
}
