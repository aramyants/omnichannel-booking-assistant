package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/meta"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/smsgate"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegram"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegramaccount"
	cloudtasksadapter "github.com/aramyants/omnichannel-booking-assistant/internal/adapters/tasks/cloudtasks"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/assistant"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/cabinet"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/config"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
	"github.com/google/uuid"
)

func openNotifications(ctx context.Context, cfg config.Config, store appStore, reader notifications.Reader, tg *telegram.Client, wa *meta.Client, logger *slog.Logger) (*notifications.Service, http.Handler, http.Handler, func(), error) {
	if !cfg.Notifications.Enabled {
		return nil, nil, nil, func() {}, nil
	}
	repo, ok := store.(notifications.Repository)
	if !ok || reader == nil || tg == nil {
		return nil, nil, nil, nil, errors.New("native notifications require durable storage, Altegio and Telegram")
	}
	scheduler, err := cloudtasksadapter.New(ctx, cloudtasksadapter.Config{ProjectID: cfg.Reminders.ProjectID, Location: cfg.Reminders.Location, Queue: cfg.Reminders.Queue, TargetURL: cfg.PublicBaseURL + "/tasks/notifications", Audience: cfg.Reminders.Audience, ServiceAccountEmail: cfg.Reminders.ServiceAccountEmail})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var account *telegramaccount.Client
	if cfg.Notifications.TelegramAccount.Enabled {
		credentials := cfg.Notifications.TelegramAccount
		account, err = telegramaccount.New(telegramaccount.Config{AppID: credentials.AppID, AppHash: credentials.AppHash, Phone: credentials.Phone, Username: credentials.Username, EncryptionKey: credentials.EncryptionKey}, repo)
		if err != nil {
			_ = scheduler.Close()
			return nil, nil, nil, nil, err
		}
	}
	var sms *smsgate.Client
	if cfg.Notifications.SMS.Enabled {
		credentials := cfg.Notifications.SMS
		sms, err = smsgate.NewClient(smsgate.Config{APIBaseURL: credentials.APIBaseURL, Username: credentials.Username, Password: credentials.Password, DeviceID: credentials.DeviceID, SIMNumber: credentials.SIMNumber}, nil)
		if err != nil {
			_ = scheduler.Close()
			return nil, nil, nil, nil, err
		}
	}
	service := &notifications.Service{Repo: repo, Scheduler: scheduler, Reader: reader, Owned: store, Sender: nativeNotificationSender{tg: tg, wa: wa, account: account, sms: sms, store: store, logger: logger, location: cfg.Altegio.Location, renderer: newAppointmentMessages(cfg), bookingURL: cfg.BusinessProfile.BookingURL, websiteURL: cfg.BusinessProfile.WebsiteURL}, ActivatedAt: cfg.Notifications.ActivatedAt, WhatsAppTemplates: cfg.Notifications.WhatsAppTemplates, TelegramAccount: account != nil, BookingPermissionSince: cfg.Notifications.BookingPermissionSince, NativeLanguage: cfg.Notifications.NativeLanguage, SMSReady: sms != nil, SMSPermissionSince: cfg.Notifications.SMSPermissionSince}
	if sms != nil {
		service.SMSStatus = func(ctx context.Context, notice notifications.Notice) (string, error) {
			phone, err := customer.NormalizePhone(notice.Snapshot.Phone)
			if err != nil {
				return "", errors.New("SMS receipt contact is unavailable")
			}
			status, err := sms.Status(ctx, nativeSMSMessageID(notice.ID, phone))
			return string(status.State), err
		}
		service.SMSFailureAlert = func(ctx context.Context, recordID, eventID string) error {
			logger.WarnContext(ctx, "SMS booking notice failed after acceptance", "record_id", recordID, "event_id", eventID)
			if cfg.Telegram.StaffChatID == "" {
				return nil
			}
			text := "An appointment notice was not delivered by SMS. Booking reference: " + recordID + ". Check the work phone's SMSGate queue, connection and SIM credit, then contact the customer in the business inbox. The assistant will not resend automatically."
			return tg.Send(ctx, messaging.Outgoing{Provider: messaging.ProviderTelegram, ExternalThreadID: cfg.Telegram.StaffChatID, Text: text})
		}
	}
	if wa != nil {
		expected := map[string]bool{}
		for key, name := range cfg.Notifications.WhatsAppTemplates {
			_, lang, ok := strings.Cut(key, ":")
			if ok && name != "" {
				expected[name+":"+lang] = true
			}
		}
		for lang, name := range cfg.WhatsApp.ReminderTemplates {
			if name != "" {
				expected[name+":"+lang] = true
			}
		}
		service.Health = &notifications.HealthGuard{Repo: repo,
			Read: func(ctx context.Context) (messaging.ChannelHealth, error) {
				return wa.ReadWhatsAppHealth(ctx, expected)
			},
			Alert: func(ctx context.Context, health messaging.ChannelHealth) error {
				logger.WarnContext(ctx, "WhatsApp messaging health changed", "blocked", health.Blocked, "proactive_paused", health.ProactivePaused)
				if cfg.Telegram.StaffChatID == "" {
					return nil
				}
				text := "✅ WhatsApp messaging is available again. Approved booking templates and customer replies can resume."
				switch {
				case health.Blocked:
					text = "🚨 Meta has restricted WhatsApp sending. Automatic WhatsApp replies are paused; incoming messages are saved for staff. Eligible booking notices continue through Telegram or SMS. Check Meta Business Support Home before reconnecting."
				case health.ProactivePaused:
					text = "⚠️ WhatsApp message quality is RED. Proactive booking templates are paused; customer-initiated replies remain available. Eligible notices use Telegram or SMS. Review complaints and notification consent in Meta."
				default:
					for _, approved := range health.Templates {
						if !approved {
							text = "⚠️ A configured WhatsApp utility template is not approved or has been paused. That template is skipped; eligible booking notices use Telegram or SMS. Customer-initiated replies remain available. Review the templates in WhatsApp Manager."
							break
						}
					}
				}
				return tg.Send(ctx, messaging.Outgoing{Provider: messaging.ProviderTelegram, ExternalThreadID: cfg.Telegram.StaffChatID, Text: text})
			},
		}
		wa.SetDeliveryGuard(service.Health.Allows)
		logger.Info("WhatsApp messaging health monitor configured")
	}
	authorizer, err := cloudtasksadapter.NewAuthorizer(cfg.Reminders.Audience, cfg.Reminders.ServiceAccountEmail)
	if err != nil {
		_ = scheduler.Close()
		return nil, nil, nil, nil, err
	}
	task := notificationTaskHandler(authorizer, service, logger)
	hook := notificationWebhookHandler(cfg.Notifications.WebhookSecret, cfg.Altegio.CompanyID, service)
	// Recovery is also invoked by the authenticated periodic scheduler job.
	recoverCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = service.Reconcile(recoverCtx)
	cancel()
	if err != nil {
		logger.Error("native notification recovery failed")
	}
	return service, hook, task, func() { _ = scheduler.Close() }, nil
}

type notificationIngester interface {
	Ingest(context.Context, notifications.Event) error
}

func notificationWebhookHandler(secret, companyID string, service notificationIngester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Altegio's Authorization header is shared globally, not specific to the
		// destination. An independent URL capability authenticates this receiver.
		received := r.URL.Query().Get("key")
		if received == "" {
			received = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(received), []byte(secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			CompanyID  json.Number `json:"company_id"`
			Resource   string      `json:"resource"`
			ResourceID json.Number `json:"resource_id"`
			Status     string      `json:"status"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.CompanyID.String() != companyID {
			http.Error(w, "wrong location", http.StatusForbidden)
			return
		}
		if body.Resource != "record" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if _, err := uuid.Parse(r.Header.Get("X-Hook-Id")); err != nil {
			http.Error(w, "missing event identity", http.StatusBadRequest)
			return
		}
		id, err := strconv.ParseInt(body.ResourceID.String(), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "bad record identity", http.StatusBadRequest)
			return
		}
		switch body.Status {
		case "create", "update", "delete":
		default:
			http.Error(w, "unsupported status", http.StatusBadRequest)
			return
		}
		// Respond within Altegio's 10-second delivery window; sends run in Tasks.
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		if err = service.Ingest(ctx, notifications.Event{ID: r.Header.Get("X-Hook-Id"), RecordID: body.ResourceID.String(), Status: body.Status}); err != nil {
			http.Error(w, "queue unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

type notificationAuthorizer interface {
	Authorize(context.Context, string) error
}
type notificationDeliverer interface {
	Deliver(context.Context, string) error
	Reconcile(context.Context) error
}

func notificationTaskHandler(auth notificationAuthorizer, service notificationDeliverer, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.Authorize(r.Context(), r.Header.Get("Authorization")) != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			EventID   string `json:"event_id"`
			Reconcile bool   `json:"reconcile"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var err error
		if body.Reconcile {
			err = service.Reconcile(r.Context())
		} else if _, parseErr := uuid.Parse(body.EventID); parseErr == nil {
			err = service.Deliver(r.Context(), body.EventID)
		} else {
			http.Error(w, "bad identity", http.StatusBadRequest)
			return
		}
		if err != nil {
			logger.ErrorContext(r.Context(), "native notification task needs retry")
			http.Error(w, "processing failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

type nativeNotificationSender struct {
	tg                     *telegram.Client
	wa                     *meta.Client
	account                *telegramaccount.Client
	sms                    *smsgate.Client
	store                  appStore
	logger                 *slog.Logger
	location               *time.Location
	renderer               appointmentmessage.Renderer
	bookingURL, websiteURL string
}

func (s nativeNotificationSender) SendNotification(ctx context.Context, target notifications.Target, notice notifications.Notice, lang string) (bool, error) {
	lang = string(appointmentmessage.ParseLanguage(lang))
	b := notice.Snapshot.Booking
	local := b.StartsAt.In(s.location)
	title := map[string]map[notifications.Purpose]string{
		"en": {notifications.BookingCreated: "Your booking is confirmed", notifications.BookingChanged: "Your booking has been updated", notifications.BookingCancelled: "Your booking has been cancelled"},
		"ru": {notifications.BookingCreated: "Ваша запись подтверждена", notifications.BookingChanged: "Ваша запись изменена", notifications.BookingCancelled: "Ваша запись отменена"},
		"hy": {notifications.BookingCreated: "Ձեր ամրագրումը հաստատված է", notifications.BookingChanged: "Ձեր ամրագրումը փոփոխված է", notifications.BookingCancelled: "Ձեր ամրագրումը չեղարկված է"},
	}[lang][notice.Purpose]
	if target.Channel == notifications.SMS {
		if s.sms == nil {
			return true, errors.New("SMS unavailable")
		}
		// Keep the same provider ID across retries, while fitting the live API's
		// ID limit. A timeout remains uncertain in the durable send ledger.
		validUntil := time.Now().UTC().Add(15 * time.Minute)
		if notice.Purpose != notifications.BookingCancelled && b.StartsAt.Before(validUntil) {
			validUntil = b.StartsAt
		}
		_, err := s.sms.Send(ctx, smsgate.Message{ID: nativeSMSMessageID(notice.ID, target.Address), Phone: target.Address,
			Text:       s.renderer.SMS(title, appointmentmessage.Appointment{StartsAt: b.StartsAt, Service: strings.Join(b.ServiceNames, ", "), Specialist: b.StaffName}, appointmentmessage.Language(lang)),
			ValidUntil: validUntil, Priority: 0})
		return errors.Is(err, smsgate.ErrRejected), err
	}
	details := strings.Join(b.ServiceNames, ", ")
	if b.StaffName != "" {
		details += " · " + b.StaffName
	}
	when := local.Format("02.01.2006 · 15:04")
	if target.Channel == notifications.WhatsApp {
		if s.wa == nil {
			return true, errors.New("WhatsApp unavailable")
		}
		err := s.wa.SendBookingTemplateWithReference(ctx, target.Address, target.TemplateID, lang, []string{title, when, details}, "native-booking:"+notice.ID)
		return errors.Is(err, meta.ErrRejected), err
	}
	appointment := appointmentmessage.Appointment{CustomerName: b.CustomerName, StartsAt: b.StartsAt, Service: strings.Join(b.ServiceNames, ", "), Specialist: b.StaffName}
	render := s.renderer.Confirmation
	switch notice.Purpose {
	case notifications.BookingChanged:
		render = s.renderer.Changed
	case notifications.BookingCancelled:
		render = s.renderer.Cancelled
	}
	text := render(appointmentmessage.Language(lang), appointment)
	msg := messaging.Outgoing{Provider: messaging.ProviderTelegram, ExternalThreadID: target.Address, Text: text}
	labels := map[string][]string{"hy": {"Առցանց ամրագրում", "Կայք և հասցե"}, "ru": {"Онлайн-запись", "Сайт и адрес"}, "en": {"Book online", "Website & directions"}}[lang]
	links := []messaging.Link{{Label: labels[0], URL: s.bookingURL}, {Label: labels[1], URL: s.websiteURL}}
	links = append(links, s.renderer.Links(appointmentmessage.Language(lang), appointment)...)
	msg = msg.WithLinks(links)
	if target.StudioAccount {
		if s.account == nil {
			return true, errors.New("studio Telegram account unavailable")
		}
		// User accounts cannot send bot inline keyboards; expose the same useful
		// destinations as plain links. Replies remain in the studio Telegram app.
		for _, link := range links {
			if link.URL != "" {
				text += "\n" + link.Label + ": " + link.URL
			}
		}
		return s.account.Send(ctx, target.Address, text, notice.ID)
	}
	err := s.tg.Send(ctx, msg)
	if err == nil && s.store != nil {
		if historyErr := s.recordTranscript(ctx, msg, notice, lang); historyErr != nil {
			s.logger.ErrorContext(ctx, "accepted native notification transcript could not be saved")
		}
	}
	var rejected *telegram.APIError
	safe := errors.As(err, &rejected) && rejected.StatusCode >= 400 && rejected.StatusCode < 500 && rejected.Code != 429
	return safe, err
}

func nativeSMSMessageID(eventID, phone string) string {
	digest := sha256.Sum256([]byte("smsgate:" + eventID + ":" + phone))
	return fmt.Sprintf("%x", digest[:16])
}

func (s nativeNotificationSender) recordTranscript(ctx context.Context, msg messaging.Outgoing, notice notifications.Notice, lang string) error {
	now := time.Now().UTC()
	cust, err := s.store.FindOrCreateByChannelIdentity(ctx, customer.ChannelIdentity{ID: id.New(), Provider: msg.Provider, ExternalUserID: msg.ExternalThreadID, DisplayName: notice.Snapshot.Booking.CustomerName, Language: lang, CreatedAt: now}, customer.Customer{ID: id.New(), Name: notice.Snapshot.Booking.CustomerName, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return err
	}
	conv, err := s.store.FindOrOpen(ctx, conversation.Conversation{ID: id.New(), CustomerID: cust.ID, Provider: msg.Provider, ExternalThreadID: msg.ExternalThreadID, State: conversation.StateAssistantActive, CreatedAt: now, UpdatedAt: now, LastMessageAt: now})
	if err != nil {
		return err
	}
	if err = s.store.Append(ctx, conversation.Message{ID: id.New(), ConversationID: conv.ID, Direction: conversation.DirectionOutbound, ContentType: messaging.ContentTypeText, Text: msg.Text, ExternalMessageID: "native:" + notice.ID, CreatedAt: now}); err != nil {
		return err
	}
	if activity, ok := s.store.(interface {
		TouchNotificationConversation(context.Context, string, time.Time) error
	}); ok {
		return activity.TouchNotificationConversation(ctx, conv.Key(), now)
	}
	return nil
}

// notificationMessages provides explicit WhatsApp opt-in inside a signed,
// customer-initiated chat. Other messages continue through the normal assistant.
type notificationMessages struct {
	service *notifications.Service
	next    telegram.MessageHandler
	wa      *meta.Client
}

// Preserve manual Business app replies through the notification command layer.
// The Meta handler applies these before customer messages so staff takeover
// can suppress an assistant answer already being composed.
func (h notificationMessages) RecordExternalReply(ctx context.Context, reply messaging.ExternalReply) error {
	recorder, ok := h.next.(interface {
		RecordExternalReply(context.Context, messaging.ExternalReply) error
	})
	if !ok {
		return errors.New("assistant business app reply recorder is not configured")
	}
	return recorder.RecordExternalReply(ctx, reply)
}

func (h notificationMessages) Handle(ctx context.Context, msg messaging.Envelope) error {
	enabled, lang, handled := whatsappNotificationPreference(msg.Content.Text, msg.Sender.Language)
	if msg.Provider != messaging.ProviderWhatsApp || !handled {
		return h.next.Handle(ctx, msg)
	}
	setPreference := func() error {
		if strings.EqualFold(strings.TrimSpace(msg.Content.Text), "STOP") {
			return h.service.BlockPhone(ctx, msg.ExternalUserID)
		}
		return h.service.LinkWhatsApp(ctx, msg.ExternalUserID, lang, enabled)
	}
	if err := setPreference(); err != nil {
		return err
	}
	text := map[string]string{
		"en": "Booking updates are enabled in English. Please use this phone number when booking. For Russian, send /notifications ru. To stop these updates, send /notifications_off.",
		"ru": "Уведомления о записи включены. Пожалуйста, указывайте этот номер телефона при записи. Чтобы отключить уведомления, отправьте /notifications_off.",
		"hy": "Ամրագրման ծանուցումները միացված են։ Խնդրում ենք ամրագրելիս նշել այս հեռախոսահամարը։ Ծանուցումներն անջատելու համար ուղարկեք /notifications_off։",
	}[lang]
	if enabled && h.service.WhatsAppTemplates[string(notifications.BookingCreated)+":"+lang] == "" {
		text = map[string]string{
			"en": "Your notification preference is saved. WhatsApp booking updates are not available in this language yet. Please use our Telegram bot for booking updates.",
			"ru": "Ваше предпочтение сохранено. Уведомления о записи на этом языке пока недоступны в WhatsApp. Пожалуйста, используйте наш Telegram-бот для уведомлений.",
			"hy": "Ձեր նախընտրությունը պահպանված է։ Հայերեն ծանուցումները հասանելի են մեր Telegram բոտում։ WhatsApp-ում անգլերեն ծանուցումների համար ուղարկեք /notifications en, իսկ ռուսերենի համար՝ /notifications ru։",
		}[lang]
	}
	if !enabled {
		text = map[string]string{
			"en": "Booking updates are disabled. To enable them again, send /notifications.",
			"ru": "Уведомления о записи отключены. Чтобы включить их снова, отправьте /notifications.",
			"hy": "Ամրագրման ծանուցումներն անջատված են։ Կրկին միացնելու համար ուղարկեք /notifications։",
		}[lang]
	}
	return h.wa.Send(ctx, messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: msg.ExternalThreadID, Text: text})
}

func whatsappNotificationPreference(text, language string) (enabled bool, lang string, handled bool) {
	lang = string(appointmentmessage.ParseLanguage(language))
	parts := strings.Fields(text)
	if len(parts) == 1 && (parts[0] == "/notifications_off" || strings.EqualFold(parts[0], "STOP")) {
		return false, lang, true
	}
	if len(parts) < 1 || len(parts) > 2 || parts[0] != "/notifications" {
		return false, lang, false
	}
	if len(parts) == 2 {
		if parts[1] != "en" && parts[1] != "ru" {
			return false, lang, false
		}
		lang = parts[1]
	}
	return true, lang, true
}

func optionalTelegramNotifications(s *notifications.Service, c *telegram.Client, staff string, identity *cabinet.Identity, app *assistant.Service) telegram.BookingNotifications {
	if s == nil {
		return nil
	}
	return telegram.NativeNotifications{Service: s, Client: c, StaffChatID: staff, Identity: identity, CabinetAssistant: app}
}
