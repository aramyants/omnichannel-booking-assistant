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
	s.offering = offerNavigation
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
	if s.offering == offerNavigation || s.offering == offerWorkflow {
		return labelsOfChoices(s.choices)
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
	return s.navigateCatalogue(ctx, sess, msgText, selectedPresentedChoice)
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
