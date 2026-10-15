package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (t *toolset) bookingVerificationPrompt(ctx context.Context, s *session) string {
	s.conv.Draft.AwaitingPhoneVerification = true
	prompt := t.verificationPrompt(ctx, s)
	// An existing challenge, a failure or a cooldown keeps its exact response.
	if prompt != cabinetText(s.language, "phone") && prompt != cabinetText(s.language, "contact") {
		return prompt
	}
	if s.conv.Provider == messaging.ProviderTelegram {
		switch s.language {
		case languageRussian:
			return "Перед записью подтвердите свой номер кнопкой Telegram. Если номер для записи другой, отправьте его с кодом страны — мы пришлём один код по SMS. Выбранное время сохранено, но ещё не забронировано. /verify_cancel — отменить проверку."
		case languageArmenian:
			return "Ամրագրելուց առաջ կոճակով հաստատեք Ձեր Telegram հեռախոսահամարը։ Եթե ամրագրման համարը այլ է, ուղարկեք այն երկրի կոդով․ SMS-ով մեկ կոդ կուղարկենք։ Ընտրված ժամը պահպանված է, բայց դեռ ամրագրված չէ։ /verify_cancel՝ ստուգումը դադարեցնելու համար։"
		default:
			return "Before booking, verify your phone with the Telegram contact button. If the booking number is different, send it with its country code and we will send one SMS code. Your selection is saved, but the time is not booked. /verify_cancel stops verification."
		}
	}
	// The phone is already known. One tap requests its SMS rather than asking
	// the customer to type the same contact again. No SMS is sent by a model.
	s.present(s.conv.Draft.Phone, "/verify_cancel")
	switch s.language {
	case languageRussian:
		return "Перед записью подтвердите номер телефона. Нажмите на номер ниже, чтобы получить один код по SMS. Выбранное время сохранено, но ещё не забронировано. /verify_cancel — отменить проверку."
	case languageArmenian:
		return "Ամրագրելուց առաջ հաստատեք հեռախոսահամարը։ Սեղմեք ներքևի համարը՝ SMS-ով մեկ կոդ ստանալու համար։ Ընտրված ժամը պահպանված է, բայց դեռ ամրագրված չէ։ /verify_cancel՝ ստուգումը դադարեցնելու համար։"
	default:
		return "Before booking, verify your phone. Tap your number below to request one SMS code. Your selection is saved, but the time is not booked. /verify_cancel stops verification."
	}
}

// A code or own contact never books anything. Restore only the proposal for
// this proved phone, display its details, and require a fresh confirmation.
func (s *Service) resumeVerifiedBooking(sess *session, phone string) (string, bool) {
	draft := sess.conv.Draft
	if draft == nil || !draft.AwaitingPhoneVerification {
		return "", false
	}
	if phone == "" || draft.Phone != phone || draft.Validate(s.now()) != nil {
		sess.conv.Draft = nil
		return "", false
	}
	draft.AwaitingPhoneVerification = false
	draft.PreparedFromMessageID = sess.incomingMessageID
	sess.offerFixed(offerBookingConfirmation)
	heading, question, minutes := "Your phone is verified. Please check your appointment:", "Nothing is booked yet. Shall I book it?", "min"
	switch sess.language {
	case languageRussian:
		heading, question = "Ваш номер подтверждён. Проверьте данные записи:", "Запись ещё не создана. Записать Вас?"
		minutes = "мин"
	case languageArmenian:
		heading, question = "Ձեր համարը հաստատված է։ Խնդրում ենք ստուգել այցի տվյալները․", "Ամրագրում դեռ չկա։ Ամրագրե՞լ։"
		minutes = "րոպե"
	}
	details := fmt.Sprintf("%s · %d %s", strings.Join(draft.ServiceNames, ", "), int(draft.Duration.Minutes()), minutes)
	if draft.PriceLabel != "" {
		details += " · " + draft.PriceLabel
	}
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n\n%s", heading,
		details, draft.StaffName,
		draft.StartsAt.In(s.tools.location).Format("02.01.2006 15:04"), draft.CustomerName, draft.Phone, question), true
}
