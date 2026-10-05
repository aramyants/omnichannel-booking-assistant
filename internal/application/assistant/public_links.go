package assistant

import (
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func publicLinkLabels(lang language) (website, booking, heading string) {
	switch lang {
	case languageArmenian:
		return "Մեր կայքը", "Ամրագրել առցանց", "Կարող եք ծանոթանալ մեր ստուդիային կամ ընտրել ծառայությունն ու ժամը առցանց։"
	case languageRussian:
		return "Наш сайт", "Записаться онлайн", "Познакомьтесь с нашей студией или выберите услугу и время онлайн."
	default:
		return "Our website", "Book online", "Explore our studio or choose your service and appointment time online."
	}
}

func (s *Service) publicLinks(lang language) []messaging.Link {
	website, booking, _ := publicLinkLabels(lang)
	var links []messaging.Link
	if s.business.BookingURL != "" {
		links = append(links, messaging.Link{Label: booking, URL: s.business.BookingURL})
	}
	if s.business.WebsiteURL != "" {
		links = append(links, messaging.Link{Label: website, URL: s.business.WebsiteURL})
	}
	return links
}

func (s *Service) publicLinkReply(sess *session, input string) (string, bool) {
	website, booking, heading := publicLinkLabels(sess.language)
	action := menuAction(input)
	if action == "contact" {
		sess.finalLinks = s.contactLinks(sess.language)
		sess.offerFixed(offerMenu)
		return heading, true
	}
	if action != "website" && action != "book_online" && strings.TrimSpace(input) != website && strings.TrimSpace(input) != booking {
		return "", false
	}
	sess.finalLinks = s.publicLinks(sess.language)
	sess.offerFixed(offerMenu)
	if len(sess.finalLinks) == 0 {
		return s.greeting(sess.language), true
	}
	return heading, true
}

func contactLabel(lang language) string {
	switch lang {
	case languageArmenian:
		return "Կապ և սոցիալական էջեր"
	case languageRussian:
		return "Контакты и соцсети"
	default:
		return "Contact & socials"
	}
}

func (s *Service) contactLinks(lang language) []messaging.Link {
	links := s.publicLinks(lang)
	for _, link := range []messaging.Link{
		{Label: "Instagram", URL: s.business.InstagramURL},
		{Label: "Facebook", URL: s.business.FacebookURL},
		{Label: "Telegram", URL: s.business.TelegramURL},
		{Label: "WhatsApp", URL: s.business.WhatsAppURL},
	} {
		if link.URL != "" {
			links = append(links, link)
		}
	}
	return links
}
