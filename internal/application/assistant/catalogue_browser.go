package assistant

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

const maxServiceDescriptionRunes = 220

type cataloguePhrases struct {
	bookHeading      string
	browseHeading    string
	categoryIncludes string
	chooseCategory   string
	chooseService    string
	unavailable      string
	duration         func(int) string
}

var cataloguePhrasebook = map[language]cataloguePhrases{
	languageEnglish: {
		bookHeading:      "🌿 Let’s find the right treatment",
		browseHeading:    "🌿 Services and prices",
		categoryIncludes: "Includes",
		chooseCategory:   "Choose a category — tap a button or send its number.",
		chooseService:    "Choose a service — tap a button or send its number.",
		unavailable:      "I couldn’t load the services just now. Please try again in a moment or choose “Talk to a person”.",
		duration:         func(minutes int) string { return fmt.Sprintf("%d min", minutes) },
	},
	languageRussian: {
		bookHeading:      "🌿 Подберём подходящую процедуру",
		browseHeading:    "🌿 Услуги и цены",
		categoryIncludes: "В категории",
		chooseCategory:   "Выберите категорию — нажмите кнопку или отправьте её номер.",
		chooseService:    "Выберите услугу — нажмите кнопку или отправьте её номер.",
		unavailable:      "Сейчас не получилось загрузить услуги. Попробуйте ещё раз через минуту или выберите «Связаться с сотрудником».",
		duration:         func(minutes int) string { return fmt.Sprintf("%d мин", minutes) },
	},
	languageArmenian: {
		bookHeading:      "🌿 Եկեք ընտրենք համապատասխան ծառայությունը",
		browseHeading:    "🌿 Ծառայություններ և գներ",
		categoryIncludes: "Ներառում է",
		chooseCategory:   "Ընտրեք բաժինը՝ սեղմեք կոճակը կամ ուղարկեք համարը։",
		chooseService:    "Ընտրեք ծառայությունը՝ սեղմեք կոճակը կամ ուղարկեք համարը։",
		unavailable:      "Այս պահին չհաջողվեց բեռնել ծառայությունները։ Փորձեք մի փոքր ուշ կամ ընտրեք «Կապվել աշխատակցի հետ»։",
		duration:         func(minutes int) string { return fmt.Sprintf("%d րոպե", minutes) },
	},
}

func catalogueSpeak(lang language) cataloguePhrases {
	if p, ok := cataloguePhrasebook[lang]; ok {
		return p
	}
	return cataloguePhrasebook[languageEnglish]
}

// present replaces every earlier lookup option with one coherent numbered
// list. Buttons and typed numbers therefore describe the same current prompt.
func (s *session) present(labels ...string) {
	s.offering = offerWhatToolsSaid
	s.candidates = choicesOf(labels...)
	s.choices = choicesOf(labels...)
}

// presentedChoices reports the numbered choices the customer can answer with
// a digit. Fixed welcome-menu choices are numbered in the greeting; model
// replies are admitted only when an actual numbered line names a live tool
// result.
func (s *session) presentedChoices(replyText string) []string {
	if s.offering == offerMenu {
		return labelsOfChoices(menuChoices(s.language))
	}
	return numberedCandidates(replyText, s.candidates)
}

func labelsOfChoices(choices []messaging.Choice) []string {
	labels := make([]string, 0, len(choices))
	for _, choice := range choices {
		if label := strings.TrimSpace(choice.Label); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func numberedCandidates(text string, candidates []messaging.Choice) []string {
	var matched []string
	seen := make(map[string]bool, len(candidates))
	for _, line := range strings.Split(text, "\n") {
		if _, ok := numberedLine(strings.TrimSpace(line)); !ok {
			continue
		}
		for _, candidate := range candidates {
			if seen[candidate.Label] || !choiceNamedInReply(line, candidate.Label, candidates) {
				continue
			}
			matched = append(matched, candidate.Label)
			seen[candidate.Label] = true
			break
		}
	}
	return matched
}

func numberedLine(line string) (int, bool) {
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits == len(line) {
		return 0, false
	}
	if line[digits] != '.' && line[digits] != ')' {
		return 0, false
	}
	position, err := strconv.Atoi(line[:digits])
	return position, err == nil && position > 0
}

// catalogueReply handles the first two high-volume journeys without a model:
// the welcome action always opens a real, current catalogue, and choosing a
// category always opens only that category. Other conversation stays with the
// assistant and its tools.
func (s *Service) catalogueReply(
	ctx context.Context,
	sess *session,
	msgText string,
	selectedPresentedChoice bool,
) (string, bool) {
	action := menuAction(msgText)
	browse := action == "book" || action == "services"
	if !browse && !selectedPresentedChoice {
		return "", false
	}
	if s.tools == nil || s.tools.scheduling == nil {
		return "", false
	}

	services, err := s.tools.scheduling.ListServices(ctx)
	if err != nil {
		if browse {
			sess.offerFixed(offerHelp)
			return catalogueSpeak(sess.language).unavailable, true
		}
		return "", false
	}

	if browse {
		categories := categoriesOf(services)
		if len(categories) == 0 {
			sess.offerFixed(offerHelp)
			return catalogueSpeak(sess.language).unavailable, true
		}
		names := make([]string, 0, len(categories))
		for _, category := range categories {
			names = append(names, category.Name)
		}
		sess.present(names...)
		return formatCategories(services, categories, sess.language, action == "book"), true
	}

	category := strings.TrimSpace(msgText)
	matching := make([]booking.Service, 0)
	for _, service := range services {
		if strings.EqualFold(strings.TrimSpace(service.Category), category) {
			matching = append(matching, service)
		}
	}
	if len(matching) == 0 {
		return "", false
	}

	names := make([]string, 0, len(matching))
	for _, service := range matching {
		names = append(names, service.Name)
	}
	sess.present(names...)
	return formatServices(category, matching, sess.language), true
}

func formatCategories(services []booking.Service, categories []serviceCategory, lang language, bookingFlow bool) string {
	p := catalogueSpeak(lang)
	heading := p.browseHeading
	if bookingFlow {
		heading = p.bookHeading
	}

	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n\n")
	for i, category := range categories {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. %s\n   %s: %s", i+1, category.Name, p.categoryIncludes,
			categoryServicePreview(services, category.Name))
	}
	b.WriteString("\n\n")
	b.WriteString(p.chooseCategory)
	return b.String()
}

func categoryServicePreview(services []booking.Service, category string) string {
	const previewLimit = 3
	var names []string
	for _, service := range services {
		if strings.EqualFold(strings.TrimSpace(service.Category), strings.TrimSpace(category)) {
			names = append(names, service.Name)
		}
	}
	if len(names) <= previewLimit {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:previewLimit], ", ") + fmt.Sprintf(" +%d", len(names)-previewLimit)
}

func formatServices(category string, services []booking.Service, lang language) string {
	p := catalogueSpeak(lang)
	var b strings.Builder
	b.WriteString("🌿 ")
	b.WriteString(category)
	b.WriteString("\n\n")
	for i, service := range services {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. %s", i+1, service.Name)

		var facts []string
		if minutes := int(service.Duration.Minutes()); minutes > 0 {
			facts = append(facts, p.duration(minutes))
		}
		if price := displayPrice(service); price != "" {
			facts = append(facts, price)
		}
		if len(facts) > 0 {
			b.WriteString("\n   ")
			b.WriteString(strings.Join(facts, " · "))
		}
		if description := localizedDescription(service.Description, lang); description != "" {
			b.WriteString("\n   ")
			b.WriteString(description)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(p.chooseService)
	return b.String()
}

func displayPrice(service booking.Service) string {
	if service.PriceMin == 0 && service.PriceMax == 0 {
		return ""
	}
	min := groupedAmount(service.PriceMin)
	if service.PriceMin == service.PriceMax {
		return strings.TrimSpace(min + " " + service.Currency)
	}
	return strings.TrimSpace(min + "–" + groupedAmount(service.PriceMax) + " " + service.Currency)
}

func groupedAmount(value float64) string {
	raw := strconv.FormatInt(int64(math.Round(value)), 10)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "\u202f" + raw[i:]
	}
	return raw
}

func localizedDescription(raw string, lang language) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	selected := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Trim(strings.TrimSpace(line), "•-–—· ")
		if line == "" || !lineMatchesLanguage(line, lang) {
			continue
		}
		selected = append(selected, line)
	}
	if len(selected) == 0 && len(lines) == 1 {
		selected = append(selected, strings.TrimSpace(lines[0]))
	}
	text := strings.Join(selected, " ")
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= maxServiceDescriptionRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxServiceDescriptionRunes-1])) + "…"
}

func lineMatchesLanguage(line string, lang language) bool {
	hasLatin := false
	for _, r := range line {
		switch {
		case unicode.Is(unicode.Armenian, r):
			return lang == languageArmenian
		case unicode.Is(unicode.Cyrillic, r):
			return lang == languageRussian
		case unicode.Is(unicode.Latin, r):
			hasLatin = true
		}
	}
	return lang == languageEnglish && hasLatin
}
