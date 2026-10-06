package appointmentmessage

import (
	"strings"
	"unicode/utf16"
)

// SMS keeps the date, treatment, specialist, address and callback number within
// three Unicode SMS segments (201 UTF-16 units). Social links and long visit
// instructions belong in the messenger confirmation, rather than paid SMS.
func (r Renderer) SMS(title string, a Appointment, lang Language) string {
	lines := []string{"E-Motion Concept", smsField(title, 42), a.StartsAt.In(r.location).Format("02.01.2006 15:04")}
	details := smsField(a.Service, 45)
	if a.Specialist != "" {
		details += " · " + smsField(a.Specialist, 20)
	}
	if details != "" {
		lines = append(lines, details)
	}
	if address := smsField(r.business.Address.In(lang), 34); address != "" {
		lines = append(lines, address)
	}
	if r.business.Phone != "" {
		lines = append(lines, smsField(r.business.Phone, 20))
	}
	return strings.Join(lines, "\n")
}

func smsField(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(utf16.Encode([]rune(text))) <= limit {
		return text
	}
	var truncated []rune
	units := 0
	for _, char := range text {
		size := 1
		if char > 0xffff {
			size = 2
		}
		if units+size > limit-1 {
			break
		}
		truncated = append(truncated, char)
		units += size
	}
	return strings.TrimSpace(string(truncated)) + "…"
}
