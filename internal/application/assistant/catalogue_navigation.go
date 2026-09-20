package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// Six entries leave room for navigation and help within WhatsApp's ten rows.
const cataloguePageSize = 6

type navigationWords struct{ back, categories, next, previous, book, askDate, coordinated string }

func navigationSpeak(lang language) navigationWords {
	switch lang {
	case languageRussian:
		return navigationWords{"← Назад", "☰ Категории", "Далее →", "← Ранее", "Выбрать услугу", "На какой день вам удобно?", "Для этой услуги сотрудник согласует основную процедуру или одновременную работу двух специалистов. Запись пока не создана."}
	case languageArmenian:
		return navigationWords{"← Հետ", "☰ Բաժիններ", "Հաջորդը →", "← Նախորդը", "Ընտրել ծառայությունը", "Ո՞ր օրը ձեզ հարմար կլինի։", "Այս ծառայության համար աշխատակիցը կհամաձայնեցնի հիմնական սեանսը կամ երկու մասնագետի համատեղ աշխատանքը։ Ամրագրում դեռ չկա։"}
	default:
		return navigationWords{"← Back", "☰ Categories", "Next →", "← Previous", "Choose treatment", "Which day would suit you?", "A team member needs to coordinate the main session or two therapists working together for this treatment. Nothing has been booked yet."}
	}
}

func requiresCoordination(service booking.Service) bool {
	category := strings.ToLower(strings.TrimSpace(service.Category))
	return category == "add more time" || category == "motion four hands"
}

func (s *Service) navigateCatalogue(ctx context.Context, sess *session, input string, selected bool) (string, bool) {
	action := menuAction(input)
	n := navigationSpeak(sess.language)
	root := action == "book" || action == "services" || input == n.categories
	if !root && !selected && input != n.back {
		return "", false
	}
	if s.tools == nil || s.tools.scheduling == nil {
		return "", false
	}
	services, err := s.tools.scheduling.ListServices(ctx)
	if err != nil || len(services) == 0 {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable, true
	}
	c := sess.conv
	recognized := root || input == n.back || input == n.next || input == n.previous || (input == n.book && c.CatalogueServiceID != "")
	for _, service := range services {
		if strings.EqualFold(service.Category, input) || (service.Category == c.CatalogueCategory && strings.EqualFold(service.Name, input)) {
			recognized = true
		}
	}
	if !recognized {
		return "", false
	}
	// Browsing explicitly abandons unconfirmed mutations, never an appointment.
	// A later "yes" must not confirm a draft from a different treatment.
	c.Draft, c.BookingChange = nil, nil
	if root {
		c.CatalogueCategory, c.CatalogueServiceID, c.CataloguePage = "", "", 0
	}
	if input == n.back {
		if c.CatalogueServiceID != "" {
			c.CatalogueServiceID = ""
		} else {
			c.CatalogueCategory, c.CataloguePage = "", 0
		}
	}
	if input == n.next {
		c.CataloguePage++
	}
	if input == n.previous && c.CataloguePage > 0 {
		c.CataloguePage--
	}
	categorySelected := false
	for _, category := range categoriesOf(services) {
		if strings.EqualFold(category.Name, input) && c.CatalogueCategory != category.Name {
			c.CatalogueCategory, c.CatalogueServiceID, c.CataloguePage = category.Name, "", 0
			categorySelected = true
			break
		}
	}
	// Match only inside the selected category. "90 min" in two categories
	// must never resolve to whichever service happened to be returned first.
	for _, service := range services {
		if !categorySelected && service.Category == c.CatalogueCategory && strings.EqualFold(service.Name, input) {
			c.CatalogueServiceID = service.ID
			break
		}
	}
	if c.CatalogueServiceID != "" {
		for _, service := range services {
			if service.ID != c.CatalogueServiceID || service.Category != c.CatalogueCategory {
				continue
			}
			if input == n.book {
				if requiresCoordination(service) {
					return s.handOver(ctx, sess)
				}
				// Keep the selected service visible in the transcript for the tool-based
				// date/staff flow; this step makes no calendar mutation.
				return navigationMenu(sess, "🌿 "+service.Name+"\n\n"+n.askDate, []string{n.back, n.categories}), true
			}
			if !root && input != n.back && input != service.Name {
				return "", false
			}
			heading := "🌿 " + service.Name
			if service.Duration > 0 {
				heading += "\n⏱ " + catalogueSpeak(sess.language).duration(int(service.Duration.Minutes()))
			}
			if price := displayPrice(service); price != "" {
				heading += "\n" + price
			}
			if description := localizedDescription(service.Description, sess.language); description != "" {
				heading += "\n\n" + description
			}
			options := []string{n.book, n.back, n.categories}
			if requiresCoordination(service) {
				heading += "\n\n" + n.coordinated
				options[0] = speak(sess.language).talkToAPerson
			}
			return navigationMenu(sess, heading, options), true
		}
		c.CatalogueServiceID = "" // a removed live service is not bookable
	}
	if c.CatalogueCategory == "" {
		categories := categoriesOf(services)
		labels := make([]string, 0, len(categories))
		for _, category := range categories {
			labels = append(labels, category.Name)
		}
		page, controls := cataloguePage(labels, &c.CataloguePage, n)
		options := append(page, controls...)
		options = append(options, speak(sess.language).myAppointments, speak(sess.language).talkToAPerson)
		return navigationMenu(sess, catalogueSpeak(sess.language).bookHeading, options), true
	}
	var labels []string
	for _, service := range services {
		if service.Category == c.CatalogueCategory {
			labels = append(labels, service.Name)
		}
	}
	if len(labels) == 0 {
		c.CatalogueCategory = ""
		return s.navigateCatalogue(ctx, sess, n.categories, true)
	}
	page, controls := cataloguePage(labels, &c.CataloguePage, n)
	options := append(page, controls...)
	options = append(options, n.back)
	var text strings.Builder
	fmt.Fprintf(&text, "🌿 %s\n\n", c.CatalogueCategory)
	for i, label := range page {
		fmt.Fprintf(&text, "%d. %s", i+1, label)
		for _, service := range services {
			if service.Category == c.CatalogueCategory && service.Name == label {
				if price := displayPrice(service); price != "" {
					fmt.Fprintf(&text, " — %s", price)
				}
				break
			}
		}
		text.WriteByte('\n')
	}
	for i, label := range options[len(page):] {
		fmt.Fprintf(&text, "%d. %s\n", len(page)+i+1, label)
	}
	text.WriteString("\n" + catalogueSpeak(sess.language).chooseService)
	sess.present(options...)
	return text.String(), true
}

func cataloguePage(labels []string, page *int, n navigationWords) ([]string, []string) {
	if *page < 0 || *page*cataloguePageSize >= len(labels) {
		*page = 0
	}
	start, end := *page*cataloguePageSize, (*page+1)*cataloguePageSize
	if end > len(labels) {
		end = len(labels)
	}
	var controls []string
	if *page > 0 {
		controls = append(controls, n.previous)
	}
	if end < len(labels) {
		controls = append(controls, n.next)
	}
	return append([]string(nil), labels[start:end]...), controls
}

func navigationMenu(sess *session, heading string, options []string) string {
	var b strings.Builder
	b.WriteString(heading + "\n\n")
	for i, label := range options {
		fmt.Fprintf(&b, "%d. %s\n", i+1, label)
	}
	hint := "Tap a button or send its number. You can also write to us."
	if sess.language == languageRussian {
		hint = "Нажмите кнопку или отправьте её номер. Можно также написать нам."
	}
	if sess.language == languageArmenian {
		hint = "Սեղմեք կոճակը կամ ուղարկեք համարը։ Կարող եք նաև գրել մեզ։"
	}
	b.WriteString("\n" + hint)
	sess.present(options...)
	return b.String()
}

func isGreeting(text string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), "!.,։? 👋")) {
	case "hi", "hello", "hey", "start", "привет", "здравствуйте", "добрый день", "բարև", "բարեւ", "բարև ձեզ", "barev", "barev dzez":
		return true
	default:
		return false
	}
}
