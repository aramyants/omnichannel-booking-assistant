package assistant

import (
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// Only an exact, unqualified answer can consume the change already shown to
// the customer. A conditional answer ("yes, but another time") stays with the
// conversational flow and cannot silently apply the previous proposal.
func changeAnswer(text string) int {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(text), "!.,։"))
	for _, lang := range languages {
		if value == strings.ToLower(speak(lang).confirmChange) {
			return 1
		}
		if value == strings.ToLower(speak(lang).leaveItAlone) {
			return -1
		}
	}
	switch value {
	case "yes", "confirm", "confirmed", "go ahead",
		"да", "подтверждаю", "да подтверждаю",
		"այո", "հաստատում եմ", "ayo", "ayo hastatum em", "ha":
		return 1
	case "no", "leave it", "keep it", "нет", "нет оставьте", "ոչ", "voch":
		return -1
	default:
		return 0
	}
}

func isChangeAnswerButton(text string) bool {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(text), "!.,։"))
	for _, lang := range languages {
		if value == strings.ToLower(speak(lang).confirmChange) || value == strings.ToLower(speak(lang).leaveItAlone) {
			return true
		}
	}
	return false
}

func unchangedAppointment(lang language) string {
	switch lang {
	case languageArmenian:
		return "Լավ, Ձեր ամրագրումն այս հարցումով չենք փոխում։"
	case languageRussian:
		return "Хорошо, по этому запросу Вашу запись не меняем."
	default:
		return "Of course. We will not change your appointment on this request."
	}
}

func expiredChangeConfirmation(lang language) string {
	switch lang {
	case languageArmenian:
		return "Այս փոփոխության հաստատումն այլևս հասանելի չէ։ Այս հարցումով Ձեր ամրագրումը չենք փոխել։ Եթե դեռ ցանկանում եք չեղարկել կամ տեղափոխել այն, գրեք մեզ։"
	case languageRussian:
		return "Это подтверждение изменения уже недоступно. По этому запросу запись не меняли. Напишите нам, если Вы всё ещё хотите отменить или перенести её."
	default:
		return "This change confirmation is no longer available. We have not changed your appointment on this request. Tell us if you still want to cancel or reschedule it."
	}
}

func pendingChangeInstruction(draft *booking.ChangeDraft) string {
	if draft == nil {
		return ""
	}
	return fmt.Sprintf("\n\nThere is already a pending %s for appointment reference %s. Its summary was prepared on an earlier customer turn. Do not prepare the same change again or reset its confirmation. If the customer explicitly agrees, call confirm_cancellation or confirm_reschedule for this stored change; do not ask for the same consent again. If their answer is conditional or asks for different details, clarify only those details. The change is not complete until the confirmation tool succeeds.", draft.Kind, draft.Reference)
}
