package appointmentmessage

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestSMSPreservesLocalTimeAndCallbackWithinThreeUnicodeSegments(t *testing.T) {
	r := New(Business{Address: LocalizedText{English: strings.Repeat("Address ", 30), Armenian: strings.Repeat("Հասցե ", 30), Russian: strings.Repeat("Адрес ", 30)}, Phone: "+37494768067"}, time.FixedZone("Yerevan", 4*60*60))
	a := Appointment{StartsAt: time.Date(2026, 10, 6, 6, 30, 0, 0, time.UTC), Service: strings.Repeat("Massage 🙂 ", 30), Specialist: strings.Repeat("Մասնագետ ", 20)}
	for _, lang := range []Language{English, Armenian, Russian} {
		text := r.SMS(strings.Repeat("Հաստատում ", 20), a, lang)
		if len(utf16.Encode([]rune(text))) > 201 || !strings.Contains(text, "06.10.2026 10:30") || !strings.HasSuffix(text, "+37494768067") {
			t.Fatalf("SMS lost facts or exceeds budget: %q", text)
		}
	}
}

func TestSMSWithoutTreatmentKeepsSpecialistWithoutDanglingSeparator(t *testing.T) {
	r := New(Business{Phone: "+37494768067"}, time.UTC)
	a := Appointment{StartsAt: time.Date(2026, 10, 11, 11, 0, 0, 0, time.UTC), Specialist: "Test specialist"}
	for _, lang := range []Language{English, Armenian, Russian} {
		text := r.SMS("Appointment update", a, lang)
		if !strings.Contains(text, "\nTest specialist\n") || strings.Contains(text, " · ") || !strings.Contains(text, "11.10.2026 11:00") {
			t.Fatalf("SMS for an appointment without treatment lost its facts: %q", text)
		}
	}
}
