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
	copy := func(en, ru, hy string) string { return strings.Join([]string{en, ru, hy}, "\n") }
	entries := map[string]copyEntry{
		"13827004": {"Motion Relax", "Motion Relax · 80 min", copy(
			"Back, both sides of the neck & shoulder area, and legs. Calm, focused bodywork.",
			"Спина, шейно-воротниковая зона с двух сторон и ноги. Спокойная работа с основными зонами напряжения.",
			"Մեջք, պարանոց-օձիքային գոտի՝ երկու կողմից, և ոտքեր։ Հանգիստ տեմպով՝ մարմնի հիմնական լարված հատվածների վրա կենտրոնացմամբ։")},
		"13827160": {"Motion Relax", "Motion Relax · 110 min", copy(
			"Back, both sides of the legs, and neck & shoulder area. An extended format with more time for each area.",
			"Спина, ноги с двух сторон и шейно-воротниковая зона. Расширенный формат с большим вниманием к каждой зоне.",
			"Մեջք, ոտքեր՝ երկու կողմից, և պարանոց-օձիքային գոտի։ Ավելի երկար ձևաչափ՝ յուրաքանչյուր հատվածին ավելի շատ ժամանակ հատկացնելու համար։")},
		"13827161": {"Motion Relax", "Motion Relax · 140 min", copy(
			"Back, legs, arms, abdomen, chest area and head. Extended bodywork from legs to head.",
			"Спина, ноги с двух сторон, руки, живот, грудная зона и голова. Работа с телом от ног до головы в одном продолжительном сеансе.",
			"Մեջք, ոտքեր՝ երկու կողմից, ձեռքեր, որովայն, կրծքավանդակի գոտի և գլուխ։ Մարմնի հետ աշխատանք՝ ոտքերից մինչև գլուխ՝ մեկ ամբողջական սեանսի ընթացքում։")},
		"13827167": {"Motion Sport", "Motion Sport · 90 min", copy(
			"Sports massage focused on the back, legs and neck & shoulder area. For active bodies.",
			"Спортивный массаж с акцентом на спину, ноги и шейно-воротниковую зону. Для физически активного тела.",
			"Սպորտային մերսում՝ մեջքի, ոտքերի և պարանոց-օձիքային գոտու ինտենսիվ աշխատանքով։ Ֆիզիկապես ակտիվ մարմնի համար։")},
		"13779299": {"Motion Sport", "Motion Sport · 120 min", copy(
			"Extended sports massage for the back, both sides of the legs, and neck & shoulder area. Focused on overloaded areas.",
			"Расширенный спортивный массаж спины, ног с двух сторон и шейно-воротниковой зоны. Акцент на перегруженных участках.",
			"Մեջքի, երկու կողմերից ոտքերի և պարանոց-օձիքային գոտու ավելի երկար սպորտային մերսում։ Շեշտը՝ ծանրաբեռնված հատվածների աշխատանքի վրա։")},
		"13827163": {"Motion Sport", "Motion Sport · 150 min", copy(
			"Full-body sports massage covering the back, legs, arms, abdomen, chest area and head. An extended intensive format.",
			"Спортивный массаж всего тела: спина, ноги, руки, живот, грудная зона и голова. Большой формат для интенсивной работы.",
			"Սպորտային մերսում ամբողջ մարմնի համար՝ մեջք, ոտքեր, ձեռքեր, որովայն, կրծքավանդակի գոտի և գլուխ։ Մեծ ձևաչափ՝ ինտենսիվ աշխատանքի համար։")},
		"13827186": {"Motion Sculpt", "Motion Sculpt · 60 min", copy(
			"Sculpting massage combining lymphatic drainage and lifting techniques. Focus on contours, back, sides and selected areas.",
			"Скульптурирующий массаж с лимфодренажными и лифтинг-техниками. Акцент на контуры тела, спину, бока и выбранные зоны.",
			"Սկուլպտուրային մերսում՝ լիմֆոդրենաժային և լիֆտինգ տեխնիկաների համադրությամբ։ Ուշադրություն՝ մարմնի ուրվագծերին, մեջքին, կողային հատվածներին և ձեր ընտրած գոտիներին։")},
		"13827188": {"Motion Sculpt", "Motion Sculpt · 90 min", copy(
			"Extended sculpting massage with more detailed work across the body. Lymphatic drainage, lifting and contouring techniques.",
			"Расширенный скульптурирующий формат с более детальной работой по зонам тела. Лимфодренажные, лифтинг- и моделирующие техники.",
			"Ավելի երկար սկուլպտուրային ձևաչափ՝ մարմնի տարբեր գոտիների ավելի մանրամասն աշխատանքով։ Լիմֆոդրենաժային, լիֆտինգ և ձևավորող տեխնիկաների համադրություն։")},
		"13815928": {"Face Motion", "Face Motion Classic · 60 min", copy(
			"A 60-minute face-focused session using classic manual massage techniques.",
			"60-минутный массаж лица с классическими ручными техниками.",
			"Դեմքի 60 րոպեանոց մերսում՝ դասական ձեռքի տեխնիկաներով։")},
		"13827244": {"Face Motion", "Face Motion Gua Sha · 60 min", copy(
			"A 60-minute face-focused session combining Gua Sha tools with manual massage techniques.",
			"60-минутный массаж лица с сочетанием техник гуаша и ручного массажа.",
			"Դեմքի 60 րոպեանոց մերսում՝ գուաշա գործիքների և ձեռքի մերսման տեխնիկաների համադրությամբ։")},
		"13827261": {"Local", "Back Motion · 50 min", copy(
			"Targeted work focused only on the back. For guests who want to concentrate on one area.",
			"Точечная работа только со спиной. Формат для тех, кто хочет сосредоточиться на одной зоне.",
			"Թիրախային աշխատանք միայն մեջքի հատվածի հետ։ Հարմար է նրանց համար, ովքեր ցանկանում են կենտրոնանալ մեկ գոտու վրա։")},
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
