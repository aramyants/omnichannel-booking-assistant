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
