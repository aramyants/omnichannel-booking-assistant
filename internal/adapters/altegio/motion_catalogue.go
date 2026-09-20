package altegio

import (
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// Owner-reviewed copy, matched by immutable Altegio service ID AND category.
// This does not create services or override live prices/durations/availability.
// The online catalogue's titles are often only "50 min", including duplicates.
func motionCatalogueCopy(s booking.Service) booking.Service {
	type copyEntry struct{ category, name, description string }
	entries := map[string]copyEntry{
		"13827004": {"Motion Relax", "Motion Relax · 80 min", ""},
		"13827160": {"Motion Relax", "Motion Relax · 110 min", ""},
		"13827161": {"Motion Relax", "Motion Relax · 140 min", ""},
		"13827167": {"Motion Sport", "Motion Sport · 90 min", ""},
		"13779299": {"Motion Sport", "Motion Sport · 120 min", ""},
		"13827163": {"Motion Sport", "Motion Sport · 150 min", ""},
		"13827186": {"Motion Sculpt", "Motion Sculpt · 60 min", ""},
		"13827188": {"Motion Sculpt", "Motion Sculpt · 90 min", ""},
		"13815928": {"Face Motion", "Face Motion Classic · 60 min", ""},
		"13827261": {"Local", "Back Motion · 50 min", ""},
		"13827262": {"Local", "Body Boost · 50 min", ""},
		"13827264": {"Local", "Head Motion · 30 min", ""},
		"13827172": {"Motion Four Hands", "Motion Duo · 70 min", "Four-hands massage with two therapists working in sync across the whole body. One customer, two therapists.\nМассаж в четыре руки: два специалиста работают одновременно по всему телу. Один клиент, два специалиста.\nՄերսում չորս ձեռքով՝ երկու մասնագետի համաժամանակյա աշխատանքով։ Մեկ հաճախորդ, երկու մասնագետ։"},
		"13827180": {"Motion Four Hands", "Motion Sport Duo · 80 min", "Four-hands sports massage with synchronized, intensive full-body work. One customer, two therapists.\nСпортивный массаж в четыре руки с синхронной интенсивной работой двух специалистов по всему телу. Один клиент, два специалиста.\nՍպորտային մերսում չորս ձեռքով՝ երկու մասնագետի համաժամանակյա ինտենսիվ աշխատանքով։ Մեկ հաճախորդ, երկու մասնագետ։"},
		"13827268": {"Add More Time", "+20 min to a main session", "Extra time for a main session only; not a standalone treatment.\nДополнительное время к основной процедуре, не отдельная услуга.\nԼրացուցիչ ժամանակ հիմնական սեանսի համար, ոչ առանձին ծառայություն։"},
		"13827269": {"Add More Time", "+40 min to a main session", "Extra time for a main session only; not a standalone treatment.\nДополнительное время к основной процедуре, не отдельная услуга.\nԼրացուցիչ ժամանակ հիմնական սեանսի համար, ոչ առանձին ծառայություն։"},
	}
	if copy, ok := entries[s.ID]; ok && strings.EqualFold(s.Category, copy.category) {
		s.Name = copy.name
		if s.Description == "" {
			s.Description = copy.description
		}
	}
	return s
}
