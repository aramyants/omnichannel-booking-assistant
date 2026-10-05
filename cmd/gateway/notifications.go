package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/meta"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/telegram"
	cloudtasksadapter "github.com/aramyants/omnichannel-booking-assistant/internal/adapters/tasks/cloudtasks"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
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
	service := &notifications.Service{Repo: repo, Scheduler: scheduler, Reader: reader, Owned: store, Sender: nativeNotificationSender{tg: tg, wa: wa, store: store, logger: logger, location: cfg.Altegio.Location, renderer: newAppointmentMessages(cfg), bookingURL: cfg.BusinessProfile.BookingURL, websiteURL: cfg.BusinessProfile.WebsiteURL}, ActivatedAt: cfg.Notifications.ActivatedAt, WhatsAppTemplates: cfg.Notifications.WhatsAppTemplates}
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
	details := strings.Join(b.ServiceNames, ", ")
	if b.StaffName != "" {
		details += " · " + b.StaffName
	}
	when := local.Format("02.01.2006 · 15:04")
	if target.Channel == notifications.WhatsApp {
		if s.wa == nil {
			return true, errors.New("WhatsApp unavailable")
		}
		err := s.wa.SendBookingTemplate(ctx, target.Address, target.TemplateID, lang, []string{title, when, details})
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

func (h notificationMessages) Handle(ctx context.Context, msg messaging.Envelope) error {
	command := strings.TrimSpace(msg.Content.Text)
	if msg.Provider != messaging.ProviderWhatsApp || (command != "/notifications" && command != "/notifications_off") {
		return h.next.Handle(ctx, msg)
	}
	enabled := command == "/notifications"
	if err := h.service.LinkWhatsApp(ctx, msg.ExternalUserID, msg.Sender.Language, enabled); err != nil {
		return err
	}
	text := "Booking notification preference saved. Notifications are sent only with an approved WhatsApp template. SMS is disabled. Use /notifications_off to disable."
	if !enabled {
		text = "WhatsApp booking notifications are disabled. Use /notifications to enable them again."
	}
	return h.wa.Send(ctx, messaging.Outgoing{Provider: messaging.ProviderWhatsApp, ExternalThreadID: msg.ExternalThreadID, Text: text})
}

func optionalTelegramNotifications(s *notifications.Service, c *telegram.Client, staff string) telegram.BookingNotifications {
	if s == nil {
		return nil
	}
	return telegram.NativeNotifications{Service: s, Client: c, StaffChatID: staff}
}
