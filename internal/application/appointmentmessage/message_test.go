package appointmentmessage

import (
	"strings"
	"testing"
	"time"
)

func TestConfirmationIsLocalizedAndShowsOnlyConfiguredFacts(t *testing.T) {
	location := time.FixedZone("Yerevan", 4*60*60)
	renderer := New(Business{
		Name: "E-Motion Concept Studio",
		Address: LocalizedText{
			English:  "Myasnikyan 1/6, Yerevan",
			Armenian: "Մյասնիկյան 1/6, Երևան",
			Russian:  "Мясникян 1/6, Ереван",
		},
		Phone:        "+374 94 768067",
		InstagramURL: "https://www.instagram.com/e.motion.concept/",
		MapURL:       "https://maps.example/studio",
	}, location)
	appointment := Appointment{
		CustomerName: "  Garik\nGrigoryan ",
		StartsAt:     time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC),
		Service:      "TOP Sport 110 min",
		Specialist:   "Yaroslava",
		Reference:    "84721",
	}

	got := renderer.Confirmation(Russian, appointment)
	for _, want := range []string{
		"✅ Garik Grigoryan, ваша запись подтверждена.",
		"Дата: 21.08.2026",
		"Время: 15:00",
		"Услуга: TOP Sport 110 min",
		"Специалист: Yaroslava",
		"Номер записи: 84721",
		"📍 Мясникян 1/6, Ереван",
		"☎️ +374 94 768067",
		"Instagram: https://www.instagram.com/e.motion.concept/",
		"Карта: https://maps.example/studio",
		"Ждём вас в E-Motion Concept Studio!",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("confirmation does not contain %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"Перед визитом:", "В студии:", "Парковка:"} {
		if strings.Contains(got, absent) {
			t.Errorf("unconfigured section %q appeared:\n%s", absent, got)
		}
	}
}

func TestReminderUsesEachLanguageAndOmitsMissingCatalogueNames(t *testing.T) {
	renderer := New(Business{
		Preparation: LocalizedText{
			English:  "Please arrive five minutes early.",
			Armenian: "Խնդրում ենք մոտենալ 5 րոպե շուտ։",
			Russian:  "Пожалуйста, приходите за 5 минут.",
		},
	}, time.UTC)
	appointment := Appointment{StartsAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC)}

	tests := []struct {
		lang Language
		want []string
	}{
		{English, []string{"A reminder about your upcoming appointment", "Date: 20.09.2026", "Before your visit:", "Please arrive five minutes early."}},
		{Armenian, []string{"Հիշեցում Ձեր առաջիկա այցի մասին", "Ամսաթիվ: 20.09.2026", "Այցից առաջ՝", "Խնդրում ենք մոտենալ 5 րոպե շուտ։"}},
		{Russian, []string{"Напоминаем о вашей предстоящей записи", "Дата: 20.09.2026", "Перед визитом:", "Пожалуйста, приходите за 5 минут."}},
	}
	for _, test := range tests {
		got := renderer.Reminder(test.lang, appointment)
		for _, want := range test.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s reminder does not contain %q:\n%s", test.lang, want, got)
			}
		}
		if strings.Contains(got, "Service:") || strings.Contains(got, "Specialist:") {
			t.Errorf("missing catalogue names were invented:\n%s", got)
		}
	}
}

func TestMissingTranslationIsOmitted(t *testing.T) {
	renderer := New(Business{
		Address: LocalizedText{English: "English-only address"},
	}, time.UTC)
	got := renderer.Confirmation(Armenian, Appointment{StartsAt: time.Now()})
	if strings.Contains(got, "English-only address") {
		t.Fatalf("another language leaked into Armenian confirmation:\n%s", got)
	}
}
