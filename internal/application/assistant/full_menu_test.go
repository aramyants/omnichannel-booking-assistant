package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestMidConversationCategoriesCannotBeReducedToThree(t *testing.T) {
	calendar := categoryCalendar()
	for i := 3; i < 9; i++ {
		calendar.services = append(calendar.services, booking.Service{ID: fmt.Sprint(i), Name: fmt.Sprint("Treatment ", i), Category: fmt.Sprint("Category ", i)})
	}
	model := &scriptedAI{responses: []ai.Response{toolResponse("categories", toolListCategories, `{}`), {Text: "Choose a category.", Choices: []string{"Face Motion", "Motion Sport", "Motion Relax"}}}}
	sender := &fakeSender{}
	svc, store := newAIService(t, model, calendar, sender)
	conv := openConversation(t, store)
	conv.CatalogueCategory, conv.CatalogueServiceID, conv.CataloguePhase = "old", "old", "detail"
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	message := incomingText("categories-full", "What else do you offer?")
	message.Sender.Language = "en"
	if err := svc.Handle(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	labels := labelsOf(sender.sent[0].Choices)
	for _, category := range categoriesOf(calendar.services) {
		if !slices.Contains(labels, category.Name) {
			t.Fatalf("missing category %q: %v", category.Name, labels)
		}
	}
	if !slices.Contains(labels, contactLabel(languageEnglish)) {
		t.Fatalf("missing grouped contacts: %v", labels)
	}
	if err := svc.Handle(t.Context(), incomingText("last-category", "Category 8")); err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || !strings.Contains(sender.sent[1].Text, "Treatment 8") {
		t.Fatalf("last category did not navigate correctly: calls=%d text=%q", model.calls, sender.sent[1].Text)
	}
}

func TestCompleteTimeGridPagesWithoutLosingTheChosenDate(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderTelegram, messaging.ProviderWhatsApp} {
		t.Run(string(provider), func(t *testing.T) {
			calendar := defaultScheduling()
			calendar.slots = nil
			day := bookingStart()
			midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
			var want []string
			for i := 0; i < 96; i++ {
				start := midnight.Add(time.Duration(i) * 15 * time.Minute)
				calendar.slots = append(calendar.slots, booking.Slot{Start: start, StaffID: "501"})
				want = append(want, start.Format("15:04"))
			}
			model := &scriptedAI{responses: []ai.Response{toolResponse("times", toolAvailableSlots, `{"staff_id":"501","service_id":"1001","date":"`+bookingDay()+`"}`), {Text: "Choose a time.", Choices: []string{"00:00", "00:15", "00:30"}}, textResponse("What is your full name?")}}
			sender := &fakeSender{}
			svc, store := newAIService(t, model, calendar, sender)
			svc.senders[provider] = sender
			deliver := func(id, text string) {
				t.Helper()
				msg := incomingText(id, text)
				msg.Provider = provider
				msg.Sender.Language = "en"
				if err := svc.Handle(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
			}
			deliver("time-grid", "Show all available times")
			seen := map[string]bool{}
			for page := 0; page < 20; page++ {
				reply := sender.sent[len(sender.sent)-1]
				if provider == messaging.ProviderWhatsApp && len(reply.Choices) > 10 {
					t.Fatalf("WhatsApp rows: %d", len(reply.Choices))
				}
				for _, choice := range reply.Choices {
					seen[choice.Label] = true
				}
				if !slices.Contains(labelsOf(reply.Choices), navigationSpeak(languageEnglish).next) {
					break
				}
				deliver(fmt.Sprint("time-page-", page), navigationSpeak(languageEnglish).next)
			}
			for _, clock := range want {
				if !seen[clock] {
					t.Fatalf("time %s unreachable", clock)
				}
			}
			deliver("last-time", "23:45")
			request := model.requests[len(model.requests)-1]
			for _, fact := range []string{bookingDay(), "501", "1001"} {
				if !strings.Contains(request.Instructions, fact) {
					t.Fatalf("selected context %q lost", fact)
				}
			}
			conv, err := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: provider, ExternalThreadID: "219847362"})
			if err != nil {
				t.Fatal(err)
			}
			if conv.CatalogueDate != bookingDay() || len(sender.sent[len(sender.sent)-1].Choices) != 0 {
				t.Fatal("contact question retained a calendar or lost exact date")
			}
		})
	}
}

type longDateCalendar struct {
	*stubScheduling
	days []time.Time
}

func (s *longDateCalendar) AvailableDates(_ context.Context, _ string) ([]time.Time, error) {
	return s.days, nil
}

func TestDatesBeyondTheFirstEightRemainReachable(t *testing.T) {
	calendar := &longDateCalendar{stubScheduling: defaultScheduling()}
	for i := 0; i < 47; i++ {
		calendar.days = append(calendar.days, bookingStart().AddDate(0, 0, i))
	}
	sender := &fakeSender{}
	svc, _ := newAIService(t, nil, calendar, sender)
	conv := conversation.Conversation{Provider: messaging.ProviderTelegram, CatalogueServiceID: "1001", CatalogueStaffID: "501", CataloguePhase: "dates"}
	sess := &session{conv: &conv, language: languageEnglish}
	n := navigationSpeak(languageEnglish)
	svc.catalogueDateMenu(t.Context(), sess, calendar.services[0], calendar.staff[0], calendar.staff, n)
	if len(sess.choices) != 43 {
		t.Fatalf("first date page choices=%d", len(sess.choices))
	}
	if _, ok := svc.navigateCatalogue(t.Context(), sess, n.next, true); !ok {
		t.Fatal("date pagination unavailable")
	}
	last := calendar.days[46].Format(buttonDateLayout)
	if !slices.Contains(labelsOf(sess.choices), last) {
		t.Fatalf("last date missing: %v", labelsOf(sess.choices))
	}
	if _, ok := svc.navigateCatalogue(t.Context(), sess, last, true); !ok {
		t.Fatal("last date selection not handled")
	}
	if conv.CatalogueDate != calendar.days[46].Format(dateLayout) || conv.CataloguePhase != "times" {
		t.Fatalf("wrong selected day: %+v", conv)
	}
}

func TestPublicMenusDeliverAllContactDestinationsWithoutAI(t *testing.T) {
	for _, lang := range languages {
		t.Run(string(lang), func(t *testing.T) {
			sender := &fakeSender{}
			svc, _ := newAIService(t, nil, categoryCalendar(), sender)
			svc.business.WebsiteURL = "https://www.motionconcept.rest/"
			svc.business.BookingURL = "https://book.altegio.me/company/1389810/"
			svc.business.InstagramURL = "https://www.instagram.com/e.motion.concept/"
			svc.business.FacebookURL = "https://www.facebook.com/profile.php?id=61593274220346"
			svc.business.TelegramURL = "https://t.me/emotion_concept_bot"
			svc.business.WhatsAppURL = "https://wa.me/37494768067"
			svc.business.TikTokURL = "https://www.tiktok.com/@emotion.concept"
			svc.business.YouTubeURL = "https://www.youtube.com/@emotion.concept"
			svc.tools.messages = appointmentmessage.New(appointmentmessage.Business{
				MapURL: "https://maps.app.goo.gl/oSW4ZYFcf66H75Wa9", YandexMapURL: "https://yandex.com/maps/-/CXezAUML",
			}, time.UTC)
			for i, text := range []string{"/start", "/contact", "/book_online", "/website"} {
				msg := incomingText(fmt.Sprint("link-", i), text)
				msg.Sender.Language = string(lang)
				if err := svc.Handle(t.Context(), msg); err != nil {
					t.Fatal(err)
				}
			}
			if len(sender.sent[0].Links) != 2 || !slices.Contains(labelsOf(sender.sent[0].Choices), contactLabel(lang)) {
				t.Fatal("welcome lacks prominent booking and contact entry")
			}
			if len(sender.sent[1].Links) != 10 || len(sender.sent[1].Choices) == 0 {
				t.Fatalf("contact links=%v choices=%v", sender.sent[1].Links, sender.sent[1].Choices)
			}
			if sender.sent[1].Links[0].URL != svc.business.BookingURL {
				t.Fatal("booking action not first")
			}
			for _, reply := range sender.sent[2:] {
				if len(reply.Links) != 2 {
					t.Fatal("public shortcut missing")
				}
			}
		})
	}
}
