package assistant

import (
	"context"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type reminderOptions interface {
	SupportsReminder(messaging.Provider, string) bool
}

func stopsReminders(text string) bool {
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, "/stopreminders") || strings.EqualFold(text, "stop") {
		return true
	}
	for _, lang := range []language{languageEnglish, languageRussian, languageArmenian} {
		_, disable, _, _, _ := reminderLabels(lang)
		if text == disable {
			return true
		}
	}
	return false
}

func reminderLabels(lang language) (enable, disable, question, enabled, disabled string) {
	switch lang {
	case languageRussian:
		return "Включить напоминания", "Без напоминаний", "Присылать здесь напоминания за 24 часа до ваших записей? Отказаться можно командой /stopreminders.", "Напоминания включены для записей, до которых больше 24 часов. Отключить: /stopreminders.", "Напоминания отключены. Ваша запись не отменена."
	case languageArmenian:
		return "Միացնել հիշեցումները", "Առանց հիշեցումների", "Ուղարկե՞լ այստեղ հիշեցումներ այցերից 24 ժամ առաջ։ Անջատելու համար՝ /stopreminders։", "Հիշեցումները միացված են այն այցերի համար, որոնց մնացել է ավելի քան 24 ժամ։ Անջատել՝ /stopreminders։", "Հիշեցումներն անջատված են։ Ձեր ամրագրումը չի չեղարկվել։"
	default:
		return "Enable reminders", "No reminders", "Send appointment reminders here 24 hours before your visits? You can opt out with /stopreminders.", "Reminders are enabled for appointments more than 24 hours away. Opt out: /stopreminders.", "Reminders are off. Your appointment has not been cancelled."
	}
}

func (t *toolset) offerReminderConsent(s *session) {
	if s.conv.Provider != messaging.ProviderWhatsApp || s.conv.ReminderOptIn {
		return
	}
	options, ok := t.reminders.(reminderOptions)
	if !ok || !options.SupportsReminder(s.conv.Provider, string(s.language)) {
		return
	}
	enable, disable, question, _, _ := reminderLabels(s.language)
	s.finalReply += "\n\n" + question
	s.present(enable, disable)
}

func (s *Service) reminderConsent(ctx context.Context, sess *session, text string) (string, bool) {
	enable, disable, _, enabled, disabled := reminderLabels(sess.language)
	stop := stopsReminders(text)
	if text != enable && text != disable && !stop {
		return "", false
	}
	if sess.conv.Provider != messaging.ProviderWhatsApp {
		return "", false
	}
	sess.conv.ReminderOptIn = text == enable
	if err := s.conversations.Save(ctx, *sess.conv); err != nil {
		return catalogueSpeak(sess.language).unavailable, true
	}
	if !sess.conv.ReminderOptIn {
		sess.offer()
		return disabled, true
	}
	if s.tools == nil || s.tools.bookings == nil {
		return catalogueSpeak(sess.language).unavailable, true
	}
	options, ok := s.tools.reminders.(reminderOptions)
	if !ok || !options.SupportsReminder(sess.conv.Provider, string(sess.language)) {
		return catalogueSpeak(sess.language).unavailable, true
	}
	bookings, err := s.tools.bookings.ListBookings(ctx, sess.customer.ID)
	if err != nil {
		return catalogueSpeak(sess.language).unavailable, true
	}
	for _, b := range bookings {
		if b.Status == booking.StatusConfirmed {
			if err := s.tools.reminders.Plan(ctx, b, *sess.conv, string(sess.language)); err != nil {
				return catalogueSpeak(sess.language).unavailable, true
			}
		}
	}
	sess.offer()
	return enabled, true
}
