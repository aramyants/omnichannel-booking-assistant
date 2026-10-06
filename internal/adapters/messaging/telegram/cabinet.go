package telegram

import "strings"

func cabinetLinkedCopy(lang string) string {
	switch strings.Split(strings.ToLower(lang), "-")[0] {
	case "hy":
		return "Ձեր հեռախոսահամարը հաստատված է։ Բեռնում ենք Ձեր ամրագրումները։"
	case "ru":
		return "Ваш номер подтверждён. Загружаем Ваши записи."
	default:
		return "Your phone is verified. We are loading your appointments."
	}
}
