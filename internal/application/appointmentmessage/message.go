// Package appointmentmessage renders the factual messages sent after a booking
// and before an upcoming visit. The renderer is deterministic: customer-facing
// appointment details do not depend on a language model after the calendar has
// accepted the booking.
package appointmentmessage

import (
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/calendar"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Language is one of the languages for which the studio supplies fixed copy.
type Language string

const (
	English  Language = "en"
	Armenian Language = "hy"
	Russian  Language = "ru"
)

// ParseLanguage maps an IETF-style language tag to a supported language.
// Unknown and empty values use English, the application's general fallback.
func ParseLanguage(tag string) Language {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	switch Language(base) {
	case Armenian:
		return Armenian
	case Russian:
		return Russian
	default:
		return English
	}
}

// LocalizedText holds business-authored text. A missing translation is omitted
// instead of leaking another language into the message or inventing a version.
type LocalizedText struct {
	English  string
	Armenian string
	Russian  string
}

// In returns the configured text in lang.
func (t LocalizedText) In(lang Language) string {
	switch lang {
	case Armenian:
		return strings.TrimSpace(t.Armenian)
	case Russian:
		return strings.TrimSpace(t.Russian)
	default:
		return strings.TrimSpace(t.English)
	}
}

// Business contains only customer-facing visit information. Every field is
// optional. Empty fields disappear from messages, so deployment configuration
// remains the authority for address, preparation and facilities.
type Business struct {
	Name         string
	Address      LocalizedText
	Phone        string
	Preparation  LocalizedText
	Amenities    LocalizedText
	InstagramURL string
	MapURL       string
	YandexMapURL string
	ParkingURL   string
}

// Appointment is the immutable snapshot used in a confirmation or reminder.
// Service and specialist are display names captured from the booking catalogue;
// provider identifiers are never shown as substitutes.
type Appointment struct {
	CustomerName string
	StartsAt     time.Time
	Service      string
	Specialist   string
	Reference    string
	CalendarURL  string
}

// Renderer formats appointment communication in the business timezone.
type Renderer struct {
	baseURL  string
	business Business
	location *time.Location
}

func (r Renderer) WithCalendar(baseURL string) Renderer { r.baseURL = baseURL; return r }
func (r Renderer) CalendarURL(b booking.Booking, lang Language) string {
	return calendar.Link(r.baseURL, b, string(lang))
}

// New returns a renderer. A nil location falls back to UTC rather than making
// notification delivery panic during a partially configured local run.
func New(business Business, location *time.Location) Renderer {
	if location == nil {
		location = time.UTC
	}
	return Renderer{business: business, location: location}
}

// Confirmation renders the message sent only after the scheduling system has
// confirmed that the appointment exists.
func (r Renderer) Confirmation(lang Language, appointment Appointment) string {
	return r.render(lang, appointment, false)
}

// Reminder renders a concise reminder without making assumptions such as
// "tomorrow"; the configured lead time may be changed independently.
func (r Renderer) Reminder(lang Language, appointment Appointment) string {
	return r.render(lang, appointment, true)
}

// Links returns the same visit actions that are printed in a confirmation,
// ordered by what a customer is most likely to need immediately. Channel
// adapters may turn the first actions into native URL buttons.
func (r Renderer) Links(lang Language, appointment Appointment) []messaging.Link {
	calendar := "Add to calendar"
	switch lang {
	case Russian:
		calendar = "Добавить в календарь"
	case Armenian:
		calendar = "Ավելացնել օրացույցում"
	}
	links := make([]messaging.Link, 0, 5)
	if appointment.CalendarURL != "" {
		links = append(links, messaging.Link{Label: calendar, URL: appointment.CalendarURL})
	}
	words := translations[lang]
	if words.googleMap == "" {
		words = translations[English]
	}
	if r.business.YandexMapURL != "" {
		links = append(links, messaging.Link{Label: words.yandexMap, URL: r.business.YandexMapURL})
	}
	if r.business.MapURL != "" {
		links = append(links, messaging.Link{Label: words.googleMap, URL: r.business.MapURL})
	}
	if r.business.InstagramURL != "" {
		links = append(links, messaging.Link{Label: words.instagram, URL: r.business.InstagramURL})
	}
	if r.business.ParkingURL != "" {
		links = append(links, messaging.Link{Label: words.parking, URL: r.business.ParkingURL})
	}
	return links
}

type labels struct {
	confirmation             string
	confirmationNamed        string
	reminder                 string
	date                     string
	time                     string
	service                  string
	specialist               string
	reference                string
	preparation              string
	amenities                string
	instagram                string
	googleMap                string
	yandexMap                string
	parking                  string
	confirmationClosing      string
	confirmationNamedClosing string
	reminderClosing          string
	reminderNamedClosing     string
}

var translations = map[Language]labels{
	English: {
		confirmation:             "✅ Your appointment is confirmed.",
		confirmationNamed:        "✅ %s, your appointment is confirmed.",
		reminder:                 "⏰ A reminder about your upcoming appointment.",
		date:                     "Date",
		time:                     "Time",
		service:                  "Service",
		specialist:               "Specialist",
		reference:                "Booking reference",
		preparation:              "Before your visit:",
		amenities:                "At the studio:",
		instagram:                "Instagram",
		googleMap:                "Google Maps",
		yandexMap:                "Yandex Maps",
		parking:                  "Parking",
		confirmationClosing:      "We look forward to seeing you!",
		confirmationNamedClosing: "We look forward to seeing you at %s!",
		reminderClosing:          "See you soon!",
		reminderNamedClosing:     "See you soon at %s!",
	},
	Armenian: {
		confirmation:             "✅ Ձեր ամրագրումը հաստատված է։",
		confirmationNamed:        "✅ %s, Ձեր ամրագրումը հաստատված է։",
		reminder:                 "⏰ Հիշեցում Ձեր առաջիկա այցի մասին։",
		date:                     "Ամսաթիվ",
		time:                     "Ժամ",
		service:                  "Ծառայություն",
		specialist:               "Մասնագետ",
		reference:                "Ամրագրման համարը",
		preparation:              "Այցից առաջ՝",
		amenities:                "Ստուդիայում՝",
		instagram:                "Instagram",
		googleMap:                "Google Maps",
		yandexMap:                "Yandex Maps",
		parking:                  "Կայանատեղի",
		confirmationClosing:      "Սիրով սպասում ենք Ձեր այցին։",
		confirmationNamedClosing: "Սիրով սպասում ենք Ձեր այցին՝ %s-ում։",
		reminderClosing:          "Մինչ հանդիպում։",
		reminderNamedClosing:     "Մինչ հանդիպում՝ %s-ում։",
	},
	Russian: {
		confirmation:             "✅ Ваша запись подтверждена.",
		confirmationNamed:        "✅ %s, ваша запись подтверждена.",
		reminder:                 "⏰ Напоминаем о вашей предстоящей записи.",
		date:                     "Дата",
		time:                     "Время",
		service:                  "Услуга",
		specialist:               "Специалист",
		reference:                "Номер записи",
		preparation:              "Перед визитом:",
		amenities:                "В студии:",
		instagram:                "Instagram",
		googleMap:                "Google Maps",
		yandexMap:                "Яндекс Карты",
		parking:                  "Парковка",
		confirmationClosing:      "Будем ждать вас!",
		confirmationNamedClosing: "Ждём вас в %s!",
		reminderClosing:          "До встречи!",
		reminderNamedClosing:     "До встречи в %s!",
	},
}

func (r Renderer) render(lang Language, appointment Appointment, reminder bool) string {
	words, ok := translations[lang]
	if !ok {
		words = translations[English]
	}

	name := inline(appointment.CustomerName)
	heading := words.confirmation
	if reminder {
		heading = words.reminder
	} else if name != "" {
		heading = replaceName(words.confirmationNamed, name)
	}

	location := r.location
	if location == nil {
		location = time.UTC
	}
	when := appointment.StartsAt.In(location)
	details := []string{
		"🗓 " + words.date + ": " + when.Format("02.01.2006"),
		"🕒 " + words.time + ": " + when.Format("15:04") + " · " + location.String(),
	}
	if service := inline(appointment.Service); service != "" {
		details = append(details, "🌿 "+words.service+": "+service)
	}
	if specialist := inline(appointment.Specialist); specialist != "" {
		details = append(details, "👤 "+words.specialist+": "+specialist)
	}
	if reference := inline(appointment.Reference); reference != "" {
		details = append(details, words.reference+": "+reference)
	}

	sections := []string{heading, strings.Join(details, "\n")}
	if appointment.CalendarURL != "" {
		label := "📅 Add to calendar"
		if lang == Russian {
			label = "📅 Добавить в календарь"
		}
		if lang == Armenian {
			label = "📅 Ավելացնել օրացույցում"
		}
		sections = append(sections, label+"\n"+appointment.CalendarURL)
	}
	if address := r.business.Address.In(lang); address != "" {
		sections = append(sections, "📍 "+address)
	}
	if phone := inline(r.business.Phone); phone != "" {
		sections = append(sections, "☎️ "+phone)
	}
	if preparation := r.business.Preparation.In(lang); preparation != "" {
		sections = append(sections, words.preparation+"\n"+preparation)
	}
	if amenities := r.business.Amenities.In(lang); amenities != "" {
		sections = append(sections, words.amenities+"\n"+amenities)
	}

	links := make([]string, 0, 3)
	if link := inline(r.business.InstagramURL); link != "" {
		links = append(links, words.instagram+": "+link)
	}
	if link := inline(r.business.MapURL); link != "" {
		links = append(links, words.googleMap+": "+link)
	}
	if link := inline(r.business.YandexMapURL); link != "" {
		links = append(links, words.yandexMap+": "+link)
	}
	if link := inline(r.business.ParkingURL); link != "" {
		links = append(links, words.parking+": "+link)
	}
	if len(links) > 0 {
		sections = append(sections, strings.Join(links, "\n"))
	}

	closing := words.confirmationClosing
	namedClosing := words.confirmationNamedClosing
	if reminder {
		closing = words.reminderClosing
		namedClosing = words.reminderNamedClosing
	}

	if reminder {
		warm := "🌿 A little time to slow down and focus on yourself. If your plans have changed, please let us know here."
		if lang == Russian {
			warm = "🌿 Немного времени для себя и спокойного отдыха. Если планы изменились, пожалуйста, напишите нам здесь."
		}
		if lang == Armenian {
			warm = "🌿 Մի փոքր ժամանակ՝ ձեզ և հանգստի համար։ Եթե ձեր ծրագրերը փոխվել են, խնդրում ենք գրել մեզ այստեղ։"
		}
		sections = append(sections, warm)
	}
	if businessName := inline(r.business.Name); businessName != "" {
		closing = replaceName(namedClosing, businessName)
	}
	sections = append(sections, closing)
	return strings.Join(sections, "\n\n")
}

func inline(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func replaceName(format, name string) string {
	return strings.Replace(format, "%s", name, 1)
}
