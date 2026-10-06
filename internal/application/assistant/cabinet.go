package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type verifiedCustomerRepository interface {
	LinkVerifiedIdentity(context.Context, customer.ChannelIdentity, customer.Customer, string) (customer.Customer, error)
}
type phoneBookingReader interface {
	ListPhoneBookings(context.Context, string, time.Time) ([]booking.Booking, error)
}
type currentBookingReader interface {
	ReadBooking(context.Context, booking.Booking) (booking.Booking, error)
}

func looksLikeCode(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) != 6 {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cabinetText(lang language, kind string) string {
	copy := map[language]map[string]string{
		languageEnglish: {
			"phone":       "To show bookings from our online form and other channels, please send the phone number used for booking, including its country code. We will send one SMS access code. To stop, send /verify_cancel.",
			"contact":     "To show your bookings from our online form and other channels, please use the button to share your own Telegram phone number. We will verify it once. This does not enable booking notifications. To stop, send /verify_cancel.",
			"code":        "Please enter the six-digit SMS access code here. It expires in five minutes. To stop, send /verify_cancel.",
			"invalid":     "That code did not match. Please check the SMS and try again.",
			"expired":     "This verification has expired. Send /verify to start again.",
			"limited":     "Please wait before trying again. Verification attempts are limited to protect your bookings.",
			"unavailable": "We could not confirm access right now. Please try again later or choose “Talk to a person”.",
			"cancelled":   "Phone verification stopped. Your bookings have not changed.",
			"heading":     "📋 Your upcoming appointments",
			"empty":       "We found no upcoming appointments for your verified phone. If you booked with a different number, send /verify to link it, or contact our team.",
			"partial":     "These are bookings linked to this chat. Verify your phone with /verify to include bookings from our online form and other channels.",
			"changes":     "For help changing a booking, choose “Talk to a person”.",
		},
		languageRussian: {
			"phone":       "Чтобы показать записи из онлайн-формы и других каналов, отправьте номер, указанный при записи, с кодом страны. Мы отправим один код доступа по SMS. Для отмены проверки отправьте /verify_cancel.",
			"contact":     "Чтобы показать Ваши записи из онлайн-формы и других каналов, поделитесь своим номером Telegram с помощью кнопки. Подтвердим его один раз. Это не включает уведомления о записи. Для отмены проверки отправьте /verify_cancel.",
			"code":        "Введите здесь шестизначный код из SMS. Он действует пять минут. Для отмены проверки отправьте /verify_cancel.",
			"invalid":     "Код не совпал. Пожалуйста, проверьте SMS и попробуйте ещё раз.",
			"expired":     "Срок проверки истёк. Отправьте /verify, чтобы начать заново.",
			"limited":     "Пожалуйста, подождите перед следующей попыткой. Число попыток ограничено для защиты Ваших записей.",
			"unavailable": "Сейчас не удалось подтвердить доступ. Попробуйте позже или выберите «Связаться с сотрудником».",
			"cancelled":   "Проверка номера остановлена. Ваши записи не изменились.",
			"heading":     "📋 Ваши предстоящие записи",
			"empty":       "По подтверждённому номеру предстоящих записей не найдено. Если Вы записывались с другим номером, отправьте /verify, чтобы подключить его, или свяжитесь с нашей командой.",
			"partial":     "Это записи, связанные с этим чатом. Подтвердите номер через /verify, чтобы также увидеть записи из онлайн-формы и других каналов.",
			"changes":     "Для помощи с изменением записи выберите «Связаться с сотрудником».",
		},
		languageArmenian: {
			"phone":       "Առցանց ձևով և այլ ալիքներով Ձեր ամրագրումները ցույց տալու համար խնդրում ենք ուղարկել ամրագրման ժամանակ նշված հեռախոսահամարը՝ երկրի կոդով։ SMS-ով կուղարկենք մուտքի մեկ կոդ։ Ստուգումը դադարեցնելու համար ուղարկեք /verify_cancel։",
			"contact":     "Առցանց ձևով և այլ ալիքներով Ձեր ամրագրումները ցույց տալու համար խնդրում ենք կոճակով ուղարկել Ձեր սեփական Telegram հեռախոսահամարը։ Այն կհաստատենք մեկ անգամ։ Սա չի միացնում ամրագրման ծանուցումները։ Ստուգումը դադարեցնելու համար ուղարկեք /verify_cancel։",
			"code":        "Խնդրում ենք այստեղ մուտքագրել SMS-ով ստացած վեցանիշ կոդը։ Այն գործում է հինգ րոպե։ Ստուգումը դադարեցնելու համար ուղարկեք /verify_cancel։",
			"invalid":     "Կոդը չի համապատասխանում։ Խնդրում ենք ստուգել SMS-ը և կրկին փորձել։",
			"expired":     "Ստուգման ժամկետն ավարտվել է։ Կրկին սկսելու համար ուղարկեք /verify։",
			"limited":     "Խնդրում ենք սպասել մինչև հաջորդ փորձը։ Ձեր ամրագրումները պաշտպանելու համար փորձերի քանակը սահմանափակված է։",
			"unavailable": "Այս պահին չհաջողվեց հաստատել հասանելիությունը։ Խնդրում ենք փորձել ավելի ուշ կամ ընտրել «Կապվել աշխատակցի հետ»։",
			"cancelled":   "Հեռախոսահամարի ստուգումը դադարեցված է։ Ձեր ամրագրումները չեն փոխվել։",
			"heading":     "📋 Ձեր առաջիկա ամրագրումները",
			"empty":       "Ձեր հաստատված հեռախոսահամարով առաջիկա ամրագրումներ չգտանք։ Եթե ամրագրել եք այլ համարով, այն կապակցելու համար ուղարկեք /verify կամ կապվեք մեր թիմի հետ։",
			"partial":     "Սրանք այս զրույցին կապված ամրագրումներն են։ Առցանց ձևով և այլ ալիքներով ամրագրումները նույնպես տեսնելու համար հաստատեք հեռախոսահամարը /verify հրամանով։",
			"changes":     "Ամրագրումը փոխելու հարցում օգնության համար ընտրեք «Կապվել աշխատակցի հետ»։",
		},
	}
	p, ok := copy[lang]
	if !ok {
		p = copy[languageEnglish]
	}
	return p[kind]
}

func (t *toolset) verificationPrompt(ctx context.Context, s *session) string {
	s.offer()
	state, err := t.identity.Pending(ctx, s.conv.Provider, s.conv.ExternalThreadID)
	if err != nil {
		return cabinetText(s.language, "unavailable")
	}
	if state == "code" {
		return cabinetText(s.language, "code")
	}
	if err := t.identity.Begin(ctx, s.conv.Provider, s.conv.ExternalThreadID); err != nil {
		return cabinetText(s.language, "unavailable")
	}
	if s.conv.Provider == messaging.ProviderTelegram {
		s.requestContact = true
		return cabinetText(s.language, "contact")
	}
	return cabinetText(s.language, "phone")
}

func (s *Service) cabinetReply(ctx context.Context, sess *session, msg messaging.Envelope) (string, bool, error) {
	if s.identity == nil {
		return "", false, nil
	}
	action := menuAction(msg.Content.Text)
	if action == "verify_cancel" {
		if err := s.identity.Cancel(ctx, msg.Provider, msg.ExternalUserID); err != nil {
			return "", true, err
		}
		sess.offer()
		return cabinetText(sess.language, "cancelled"), true, nil
	}
	if action == "verify" {
		// An explicit relink starts a new proof without deleting the old profile.
		if err := s.identity.Cancel(ctx, msg.Provider, msg.ExternalUserID); err != nil {
			return "", true, err
		}
		return s.tools.verificationPrompt(ctx, sess), true, nil
	}
	if action == "appointments" {
		if sess.customer.VerifiedPhone == "" {
			return s.tools.verificationPrompt(ctx, sess), true, nil
		}
		return s.appointmentList(ctx, sess), true, nil
	}
	state, err := s.identity.Pending(ctx, msg.Provider, msg.ExternalUserID)
	if err != nil {
		return "", false, err
	}
	if looksLikeCode(msg.Content.Text) && (state == "locked" || state == "expired") {
		kind := "expired"
		if state == "locked" {
			kind = "limited"
		}
		return cabinetText(sess.language, kind), true, nil
	}
	if state == "code" && looksLikeCode(msg.Content.Text) {
		phone, err := s.identity.Confirm(ctx, msg.Provider, msg.ExternalUserID, strings.TrimSpace(msg.Content.Text))
		if err != nil {
			kind := "unavailable"
			switch {
			case errors.Is(err, cabinet.ErrInvalid):
				kind = "invalid"
			case errors.Is(err, cabinet.ErrLimited):
				kind = "limited"
			case errors.Is(err, cabinet.ErrExpired):
				kind = "expired"
			}
			sess.offer()
			return cabinetText(sess.language, kind), true, nil
		}
		if linker, ok := s.customers.(verifiedCustomerRepository); ok {
			sess.customer, err = linker.LinkVerifiedIdentity(ctx, customer.ChannelIdentity{Provider: msg.Provider, ExternalUserID: msg.ExternalUserID}, sess.customer, phone)
			if err != nil {
				return "", true, err
			}
		}
		sess.customer.VerifiedPhone = phone
		sess.conv.CustomerID = sess.customer.ID
		sess.conv.Draft = nil
		sess.conv.BookingChange = nil
		return s.appointmentList(ctx, sess), true, nil
	}
	if state == "phone" {
		if _, err := customer.NormalizePhone(msg.Content.Text); err == nil {
			err := s.identity.Send(ctx, msg.Provider, msg.ExternalUserID, msg.Content.Text)
			if err != nil {
				kind := "unavailable"
				if errors.Is(err, cabinet.ErrLimited) {
					kind = "limited"
				}
				return cabinetText(sess.language, kind), true, nil
			}
			sess.offer()
			return cabinetText(sess.language, "code"), true, nil
		}
	}
	return "", false, nil
}

func (t *toolset) customerBookings(ctx context.Context, cust customer.Customer) ([]booking.Booking, error) {
	local, err := t.bookings.ListBookings(ctx, cust.ID)
	if err != nil {
		return nil, err
	}
	byRef := map[string]booking.Booking{}
	for _, b := range local {
		if reader, ok := t.scheduling.(currentBookingReader); ok && b.ManagementToken != "" && b.StartsAt.After(t.now()) {
			current, err := reader.ReadBooking(ctx, b)
			if err != nil {
				return nil, err
			}
			b = current
		}
		byRef[b.ExternalID] = b
	}
	if reader, ok := t.scheduling.(phoneBookingReader); ok && cust.VerifiedPhone != "" {
		live, err := reader.ListPhoneBookings(ctx, cust.VerifiedPhone, t.now())
		if err != nil {
			return nil, err
		}
		for _, b := range live {
			if old, ok := byRef[b.ExternalID]; ok {
				b.ID = old.ID
				b.CustomerID = old.CustomerID
				b.ManagementToken = old.ManagementToken
			} else {
				b.ID = "native_" + b.ExternalID
				b.CustomerID = cust.ID
			}
			byRef[b.ExternalID] = b
		}
	}
	all := make([]booking.Booking, 0, len(byRef))
	for _, b := range byRef {
		all = append(all, b)
	}
	slices.SortFunc(all, func(a, b booking.Booking) int { return a.StartsAt.Compare(b.StartsAt) })
	return all, nil
}

func (s *Service) appointmentList(ctx context.Context, sess *session) string {
	sess.offer()
	booked, err := s.tools.customerBookings(ctx, sess.customer)
	if err != nil {
		return cabinetText(sess.language, "unavailable")
	}
	var lines []string
	for _, b := range booked {
		if b.Status != booking.StatusConfirmed || !b.StartsAt.After(s.now()) {
			continue
		}
		services := strings.Join(b.ServiceNames, ", ")
		if services == "" {
			names, _ := s.tools.catalogueNames(ctx)
			for _, id := range b.ServiceIDs {
				if name := names[id]; name != "" {
					if services != "" {
						services += ", "
					}
					services += name
				}
			}
		}
		lines = append(lines, fmt.Sprintf("📅 %s · %s\n%s\n%s · #%s", b.StartsAt.In(s.business.Location).Format("02.01.2006"), b.StartsAt.In(s.business.Location).Format("15:04"), services, b.StaffName, b.ExternalID))
	}
	if len(lines) == 0 {
		return cabinetText(sess.language, "empty")
	}
	sess.present(speak(sess.language).talkToAPerson)
	return cabinetText(sess.language, "heading") + "\n\n" + strings.Join(lines, "\n\n") + "\n\n" + cabinetText(sess.language, "changes")
}
