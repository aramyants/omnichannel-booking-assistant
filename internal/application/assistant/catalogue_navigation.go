package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Six entries leave room for navigation and help within WhatsApp's ten rows.
const cataloguePageSize = 6

func cataloguePageSizeFor(provider messaging.Provider) int {
	if provider == messaging.ProviderTelegram {
		return 40
	}
	return cataloguePageSize
}

type navigationWords struct {
	back, categories, next, previous, book, askDate, chooseStaff, noDates, coordinated string
}

func navigationSpeak(lang language) navigationWords {
	switch lang {
	case languageRussian:
		return navigationWords{back: "← Назад", categories: "☰ Категории", next: "Далее →", previous: "← Ранее", book: "Выбрать услугу", askDate: "Выберите удобную дату.", chooseStaff: "Свободное время зависит от специалиста. Чьё расписание проверить?", noDates: "У этого специалиста сейчас нет доступных дат. Выберите другого специалиста.", coordinated: "Для этой услуги сотрудник согласует основную процедуру или одновременную работу двух специалистов. Запись пока не создана."}
	case languageArmenian:
		return navigationWords{back: "← Հետ", categories: "☰ Բաժիններ", next: "Հաջորդը →", previous: "← Նախորդը", book: "Ընտրել ծառայությունը", askDate: "Ընտրեք ձեզ հարմար օրը։", chooseStaff: "Ազատ ժամերը կախված են մասնագետից։ Ո՞ր մասնագետի գրաֆիկը ստուգենք։", noDates: "Այս մասնագետի մոտ այժմ ազատ օրեր չկան։ Ընտրեք մեկ այլ մասնագետի։", coordinated: "Այս ծառայության համար աշխատակիցը կհամաձայնեցնի հիմնական սեանսը կամ երկու մասնագետի համատեղ աշխատանքը։ Ամրագրում դեռ չկա։"}
	default:
		return navigationWords{back: "← Back", categories: "☰ Categories", next: "Next →", previous: "← Previous", book: "Choose treatment", askDate: "Choose a date that suits you.", chooseStaff: "Availability depends on the specialist. Whose schedule should we check?", noDates: "This specialist has no open dates right now. Choose another specialist.", coordinated: "A team member needs to coordinate the main session or two therapists working together for this treatment. Nothing has been booked yet."}
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
		if text, ok := s.catalogueStaffChoice(ctx, sess, services, input, selected, n); ok {
			return text, true
		}
		return "", false
	}
	// Browsing explicitly abandons unconfirmed mutations, never an appointment.
	// A later "yes" must not confirm a draft from a different treatment.
	c.Draft, c.BookingChange = nil, nil
	if root {
		c.CatalogueCategory, c.CatalogueServiceID, c.CatalogueStaffID, c.CataloguePage = "", "", "", 0
		c.CataloguePhase, c.CatalogueDate = "", ""
	}
	if input == n.back {
		switch c.CataloguePhase {
		case "times":
			c.CatalogueDate, c.CataloguePhase, c.CataloguePage = "", "dates", 0
		case "dates", "staff":
			c.CatalogueStaffID, c.CatalogueDate, c.CataloguePhase, c.CataloguePage = "", "", "detail", 0
		default:
			if c.CatalogueServiceID != "" {
				c.CatalogueServiceID, c.CatalogueStaffID = "", ""
				c.CataloguePhase, c.CatalogueDate, c.CataloguePage = "", "", 0
			} else {
				c.CatalogueCategory, c.CataloguePage = "", 0
			}
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
			c.CatalogueCategory, c.CatalogueServiceID, c.CatalogueStaffID, c.CataloguePage = category.Name, "", "", 0
			c.CataloguePhase, c.CatalogueDate = "", ""
			categorySelected = true
			break
		}
	}
	// Match only inside the selected category. "90 min" in two categories
	// must never resolve to whichever service happened to be returned first.
	for _, service := range services {
		if !categorySelected && service.Category == c.CatalogueCategory && strings.EqualFold(service.Name, input) {
			c.CatalogueServiceID, c.CatalogueStaffID = service.ID, ""
			c.CataloguePhase, c.CatalogueDate, c.CataloguePage = "detail", "", 0
			break
		}
	}
	if c.CataloguePhase == "times" {
		service := booking.Service{ID: c.CatalogueServiceID}
		for _, candidate := range services {
			if candidate.ID == service.ID {
				service = candidate
				break
			}
		}
		return s.catalogueTimeMenu(ctx, sess, service, n), true
	}
	if c.CataloguePhase == "dates" && c.CatalogueStaffID != "" {
		service := booking.Service{ID: c.CatalogueServiceID}
		for _, candidate := range services {
			if candidate.ID == service.ID {
				service = candidate
				break
			}
		}
		staff, err := s.tools.staffForService(ctx, service.ID)
		if err != nil {
			sess.offerFixed(offerHelp)
			return catalogueSpeak(sess.language).unavailable, true
		}
		for _, person := range staff {
			if person.ID == c.CatalogueStaffID {
				return s.catalogueDateMenu(ctx, sess, service, person, staff, n), true
			}
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
				return s.catalogueStaffMenu(ctx, sess, service, n), true
			}
			if c.CataloguePhase == "staff" {
				return s.catalogueStaffMenu(ctx, sess, service, n), true
			}
			if c.CataloguePhase == "dates" && c.CatalogueStaffID != "" {
				staff, err := s.tools.staffForService(ctx, service.ID)
				if err != nil {
					sess.offerFixed(offerHelp)
					return catalogueSpeak(sess.language).unavailable, true
				}
				for _, person := range staff {
					if person.ID == c.CatalogueStaffID {
						return s.catalogueDateMenu(ctx, sess, service, person, staff, n), true
					}
				}
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
		c.CatalogueServiceID, c.CatalogueStaffID = "", "" // a removed live service is not bookable
	}
	if c.CatalogueCategory == "" {
		categories := categoriesOf(services)
		labels := make([]string, 0, len(categories))
		for _, category := range categories {
			labels = append(labels, category.Name)
		}
		page, controls := catalogueRootPage(labels, &c.CataloguePage, n, c.Provider)
		options := append(page, controls...)
		options = append(options, speak(sess.language).myAppointments, speak(sess.language).talkToAPerson, contactLabel(sess.language))
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
	page, controls := cataloguePageFor(labels, &c.CataloguePage, n, c.Provider)
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
	text.WriteString("\n" + catalogueSpeak(sess.language).chooseService)
	sess.present(options...)
	return text.String(), true
}

// catalogueStaffChoice advances a button-driven booking from specialist to
// service-qualified dates. It runs before the model so every channel gets the
// same complete choices and the customer never has to guess why a specialist
// is being asked for.
func (s *Service) catalogueStaffChoice(
	ctx context.Context,
	sess *session,
	services []booking.Service,
	input string,
	selected bool,
	n navigationWords,
) (string, bool) {
	if !selected || (sess.conv.CatalogueServiceID == "" && sess.conv.CatalogueStaffID == "") {
		return "", false
	}
	var service booking.Service
	for _, candidate := range services {
		if candidate.ID == sess.conv.CatalogueServiceID {
			service = candidate
			break
		}
	}
	if service.ID == "" && sess.conv.CatalogueServiceID != "" {
		return "", false
	}
	if sess.conv.CataloguePhase == "dates" && sess.conv.CatalogueStaffID != "" {
		dates, err := s.tools.datesForService(ctx, sess.conv.CatalogueStaffID, service.ID)
		if err != nil {
			return "", false
		}
		for _, day := range dates {
			if day.Format(buttonDateLayout) == input {
				sess.conv.Draft, sess.conv.BookingChange = nil, nil
				sess.conv.CatalogueDate, sess.conv.CataloguePhase, sess.conv.CataloguePage = day.Format(dateLayout), "times", 0
				return s.catalogueTimeMenu(ctx, sess, service, n), true
			}
		}
		return "", false
	}
	staff, err := s.tools.staffForService(ctx, service.ID)
	if err != nil {
		return "", false
	}
	for _, person := range staff {
		if person.Bookable && strings.EqualFold(person.Name, strings.TrimSpace(input)) {
			sess.conv.CatalogueStaffID = person.ID
			sess.conv.CataloguePhase, sess.conv.CataloguePage = "dates", 0
			return s.catalogueDateMenu(ctx, sess, service, person, staff, n), true
		}
	}
	return "", false
}

func (s *Service) catalogueStaffMenu(ctx context.Context, sess *session, service booking.Service, n navigationWords) string {
	staff, err := s.tools.staffForService(ctx, service.ID)
	if err != nil {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable
	}
	bookable := make([]booking.Staff, 0, len(staff))
	for _, person := range staff {
		if person.Bookable {
			bookable = append(bookable, person)
		}
	}
	if len(bookable) == 0 {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable
	}
	if len(bookable) == 1 {
		sess.conv.CatalogueStaffID = bookable[0].ID
		sess.conv.CataloguePhase, sess.conv.CataloguePage = "dates", 0
		return s.catalogueDateMenu(ctx, sess, service, bookable[0], staff, n)
	}
	sess.conv.CatalogueStaffID = ""
	sess.conv.CataloguePhase = "staff"
	labels := make([]string, 0, len(bookable)+2)
	for _, person := range bookable {
		labels = append(labels, person.Name)
	}
	page, controls := cataloguePageFor(labels, &sess.conv.CataloguePage, n, sess.conv.Provider)
	labels = append(page, controls...)
	labels = append(labels, n.back, n.categories)
	return navigationMenu(sess, "🌿 "+service.Name+"\n\n"+n.chooseStaff, labels)
}

func (s *Service) catalogueDateMenu(
	ctx context.Context,
	sess *session,
	service booking.Service,
	person booking.Staff,
	staff []booking.Staff,
	n navigationWords,
) string {
	dates, err := s.tools.datesForService(ctx, person.ID, service.ID)
	if err != nil {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable
	}
	labels := make([]string, 0, len(dates)+4)
	for _, day := range dates {
		labels = append(labels, day.Format(buttonDateLayout))
	}
	if len(labels) == 0 {
		sess.conv.CatalogueStaffID = ""
		sess.conv.CataloguePhase, sess.conv.CataloguePage = "staff", 0
		for _, candidate := range staff {
			if candidate.Bookable {
				labels = append(labels, candidate.Name)
			}
		}
		page, controls := cataloguePageFor(labels, &sess.conv.CataloguePage, n, sess.conv.Provider)
		labels = append(page, controls...)
		labels = append(labels, n.back, n.categories)
		return navigationMenu(sess, n.noDates, labels)
	}
	page, controls := cataloguePageFor(labels, &sess.conv.CataloguePage, n, sess.conv.Provider)
	labels = append(page, controls...)
	labels = append(labels, n.back, n.categories)
	return navigationMenu(sess, "🌿 "+service.Name+" · "+person.Name+"\n\n"+n.askDate, labels)
}

func cataloguePageFor(labels []string, page *int, n navigationWords, provider messaging.Provider) ([]string, []string) {
	size := cataloguePageSizeFor(provider)
	return cataloguePageWithSize(labels, page, n, size)
}

func catalogueRootPage(labels []string, page *int, n navigationWords, provider messaging.Provider) ([]string, []string) {
	size := cataloguePageSizeFor(provider)
	if provider != messaging.ProviderTelegram {
		size--
	} // room for three root actions
	return cataloguePageWithSize(labels, page, n, size)
}

func cataloguePageWithSize(labels []string, page *int, n navigationWords, size int) ([]string, []string) {
	if *page < 0 || *page*size >= len(labels) {
		*page = 0
	}
	start, end := *page*size, (*page+1)*size
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

func (s *Service) catalogueTimeMenu(ctx context.Context, sess *session, service booking.Service, n navigationWords) string {
	day, err := time.ParseInLocation(dateLayout, sess.conv.CatalogueDate, s.tools.location)
	if err != nil {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable
	}
	slots, err := s.tools.slotsForService(ctx, sess.conv.CatalogueStaffID, day, service.ID)
	if err != nil {
		sess.offerFixed(offerHelp)
		return catalogueSpeak(sess.language).unavailable
	}
	labels := remainingTimes(slots, s.tools.now(), s.tools.location)
	page, controls := cataloguePageFor(labels, &sess.conv.CataloguePage, n, sess.conv.Provider)
	options := append(page, controls...)
	options = append(options, n.back, n.categories)
	heading := "Available times"
	if sess.language == languageArmenian {
		heading = "Հասանելի ժամերը"
	}
	if sess.language == languageRussian {
		heading = "Доступное время"
	}
	if len(labels) == 0 {
		heading = "There are no remaining times on this date. Choose another date."
		if sess.language == languageArmenian {
			heading = "Այս օրը հասանելի ժամեր չկան։ Խնդրում ենք ընտրել այլ օր։"
		}
		if sess.language == languageRussian {
			heading = "На эту дату свободного времени нет. Выберите другой день."
		}
	}
	if service.Name != "" {
		heading = service.Name + "\n\n" + heading
	}
	return navigationMenu(sess, heading+" · "+day.Format("02.01.2006"), options)
}

func remainingTimes(slots []booking.Slot, now time.Time, location *time.Location) []string {
	var labels []string
	seen := map[string]bool{}
	for _, slot := range slots {
		if slot.Start.Before(now) {
			continue
		}
		label := slot.Start.In(location).Format("15:04")
		if !seen[label] {
			labels = append(labels, label)
			seen[label] = true
		}
	}
	slices.Sort(labels)
	return labels
}

func navigationMenu(sess *session, heading string, options []string) string {
	sess.present(options...)
	return strings.TrimSpace(heading)
}

func isGreeting(text string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(text), "!.,։? 👋")) {
	case "hi", "hello", "hey", "start", "привет", "здравствуйте", "добрый день", "բարև", "բարեւ", "բարև ձեզ", "barev", "barev dzez":
		return true
	default:
		return false
	}
}
