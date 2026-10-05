package telegram

import (
	"context"
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"strconv"
	"strings"
)

// NativeNotifications runs only after the Telegram webhook secret is checked.
type NativeNotifications struct {
	Service     *notifications.Service
	Client      *Client
	StaffChatID string
}

func (h NativeNotifications) HandleTelegram(ctx context.Context, body []byte) (bool, error) {
	var u update
	if err := json.Unmarshal(body, &u); err != nil {
		return false, nil
	}
	m := u.Message
	if m == nil || m.Chat == nil || m.From == nil {
		return false, nil
	}
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	command := strings.Fields(m.Text)
	if len(command) > 0 {
		command[0] = strings.Split(command[0], "@")[0]
	}
	if chatID == h.StaffChatID && len(command) > 0 {
		switch command[0] {
		case "/notification_status":
			row, err := h.Service.Repo.GetNotification(ctx, "policy")
			if err != nil {
				return true, err
			}
			pending, err := h.Service.Repo.PendingNotifications(ctx)
			if err != nil {
				return true, err
			}
			order := "Telegram → WhatsApp"
			if row.Order == "whatsapp" {
				order = "WhatsApp → Telegram"
			}
			account := "անջատված"
			if h.Service.TelegramAccount {
				account = "միացված"
			}
			_, err = h.Client.sendWithMarkup(ctx, chatID, "Ծանուցումների հերթականությունը՝ "+order+"։ SMS-ն անջատված է։ Սպասող իրադարձություններ՝ "+strconv.Itoa(len(pending))+"։\nՍտուդիայի Telegram հաշիվ՝ "+account+"։ Կապակցված հաճախորդները ծանուցումներ ստանում են բոտից։\n/notification_order telegram կամ /notification_order whatsapp\nՀաճախորդի համաձայնությունն արձանագրելու համար՝ /notification_allow +374… hy|ru|en\nԾանուցումներն անջատելու համար՝ /notification_block +374…", nil)
			return true, err
		case "/notification_allow":
			if len(command) != 3 {
				_, err := h.Client.sendWithMarkup(ctx, chatID, "Միայն հաճախորդի խնդրանքը հաստատելուց հետո՝ /notification_allow +374… hy|ru|en", nil)
				return true, err
			}
			if err := h.Service.AllowPhone(ctx, command[1], command[2]); err != nil {
				_, sendErr := h.Client.sendWithMarkup(ctx, chatID, "Խնդրում ենք ստուգել հեռախոսահամարը և լեզուն՝ hy, ru կամ en։", nil)
				return true, sendErr
			}
			_, err := h.Client.sendWithMarkup(ctx, chatID, "Հաճախորդի՝ ամրագրման ծանուցումներ ստանալու խնդրանքն արձանագրված է։ SMS-ն անջատված է։", nil)
			return true, err
		case "/notification_block":
			if len(command) != 2 {
				_, err := h.Client.sendWithMarkup(ctx, chatID, "/notification_block +374…", nil)
				return true, err
			}
			if err := h.Service.BlockPhone(ctx, command[1]); err != nil {
				_, sendErr := h.Client.sendWithMarkup(ctx, chatID, "Խնդրում ենք ստուգել հեռախոսահամարը։", nil)
				return true, sendErr
			}
			_, err := h.Client.sendWithMarkup(ctx, chatID, "Այս համարի ամրագրման ավտոմատ ծանուցումներն անջատված են։", nil)
			return true, err
		case "/notification_order":
			if len(command) != 2 || (command[1] != "telegram" && command[1] != "whatsapp") {
				_, err := h.Client.sendWithMarkup(ctx, chatID, "/notification_order telegram կամ /notification_order whatsapp", nil)
				return true, err
			}
			err := h.Service.Repo.TransactNotifications(ctx, []string{"policy"}, func(rows map[string]*notifications.Entry) error {
				rows["policy"].Kind = "policy"
				rows["policy"].Order = command[1]
				return nil
			})
			if err != nil {
				return true, err
			}
			_, err = h.Client.sendWithMarkup(ctx, chatID, "Ծանուցումների հերթականությունը պահպանված է։ SMS-ն անջատված է։", nil)
			return true, err
		}
		return false, nil
	}
	if m.Chat.Type != "private" || m.Chat.ID != m.From.ID {
		return false, nil
	}
	lang := m.From.LanguageCode
	on := len(command) > 0 && (command[0] == "/notifications" || (command[0] == "/start" && len(command) > 1 && command[1] == "notifications"))
	off := len(command) > 0 && command[0] == "/notifications_off"
	if off {
		if err := h.Service.UnlinkTelegram(ctx, chatID); err != nil {
			return true, err
		}
		return true, h.Client.contactMessage(ctx, chatID, notificationCopy(lang, "off"), true)
	}
	if on {
		if err := h.Service.RequestContact(ctx, chatID, lang); err != nil {
			return true, err
		}
		return true, h.Client.contactMessage(ctx, chatID, notificationCopy(lang, "request"), false)
	}
	if m.Contact != nil {
		if m.Contact.UserID != m.From.ID {
			return true, h.Client.contactMessage(ctx, chatID, notificationCopy(lang, "own"), true)
		}
		if err := h.Service.LinkTelegram(ctx, chatID, m.Contact.PhoneNumber); err != nil {
			return true, h.Client.contactMessage(ctx, chatID, notificationCopy(lang, "retry"), true)
		}
		return true, h.Client.contactMessage(ctx, chatID, notificationCopy(lang, "linked"), true)
	}
	return false, nil
}
func (c *Client) contactMessage(ctx context.Context, chat, text string, remove bool) error {
	markup := map[string]any{"remove_keyboard": true}
	if !remove {
		markup = map[string]any{"keyboard": [][]any{{map[string]any{"text": "Share my phone / Поделиться номером / Ուղարկել հեռախոսահամարը", "request_contact": true}}}, "resize_keyboard": true, "one_time_keyboard": true}
	}
	_, err := c.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text, "reply_markup": markup})
	return err
}
func notificationCopy(lang, kind string) string {
	lang = strings.Split(strings.ToLower(lang), "-")[0]
	copy := map[string]map[string]string{
		"en": {"request": "Receive booking confirmations and changes here. To enable them, use the button to share your own Telegram phone number, then use the same number in our booking form. Sharing enables booking notifications; /notifications_off disables them. SMS is currently disabled.", "linked": "Your phone is linked. We will send new booking confirmations and changes here when your booking uses this number. /notifications_off disables notifications.", "own": "Please share your own Telegram contact, using /notifications and the phone button.", "retry": "We could not link this contact. Use /notifications and share your own phone within 10 minutes. If it is already linked to another account, please contact the studio.", "off": "Telegram booking notifications are disabled. Use /notifications to enable them again."},
		"ru": {"request": "Получайте подтверждения и изменения записи здесь. Для подключения нажмите кнопку и отправьте свой номер Telegram, затем указывайте этот же номер в форме записи. Отправляя номер, Вы включаете уведомления о записи. /notifications_off отключает их. SMS пока отключены.", "linked": "Ваш номер подключён. Мы будем отправлять сюда подтверждения новых записей и изменения, если в записи указан этот номер. /notifications_off отключает уведомления.", "own": "Пожалуйста, отправьте свой контакт Telegram через /notifications и кнопку отправки номера.", "retry": "Не удалось подключить контакт. Отправьте /notifications и свой номер в течение 10 минут. Если номер уже связан с другим аккаунтом, пожалуйста, свяжитесь со студией.", "off": "Уведомления о записи в Telegram отключены. /notifications включает их снова."},
		"hy": {"request": "Ամրագրումների հաստատումներն ու փոփոխությունները կարող եք ստանալ այստեղ։ Միացնելու համար կոճակով ուղարկեք Ձեր Telegram հեռախոսահամարը և ամրագրման ձևում նշեք նույն համարը։ Համարն ուղարկելով՝ միացնում եք ամրագրման ծանուցումները։ /notifications_off հրամանով կարող եք անջատել դրանք։ SMS-ն այս պահին անջատված է։", "linked": "Ձեր հեռախոսահամարը կապակցված է։ Նոր ամրագրումների հաստատումներն ու փոփոխությունները կուղարկենք այստեղ, եթե ամրագրելիս նշեք նույն համարը։ /notifications_off հրամանով կարող եք անջատել ծանուցումները։", "own": "Խնդրում ենք /notifications հրամանով և հեռախոսահամարի կոճակով ուղարկել Ձեր սեփական Telegram կոնտակտը։", "retry": "Չհաջողվեց կապակցել կոնտակտը։ Խնդրում ենք ուղարկել /notifications և 10 րոպեի ընթացքում՝ Ձեր սեփական համարը։ Եթե համարը կապված է այլ հաշվի հետ, խնդրում ենք կապվել ստուդիայի հետ։", "off": "Telegram-ում ամրագրման ծանուցումներն անջատված են։ /notifications հրամանով կարող եք կրկին միացնել դրանք։"},
	}
	if copy[lang] == nil {
		lang = "en"
	}
	return copy[lang][kind]
}
