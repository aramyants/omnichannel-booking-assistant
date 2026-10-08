package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/google/uuid"
)

var ErrBusy = errors.New("notification work is leased")

// Event contains routing hints only. Customer data is always read from Altegio.
type Event struct {
	ID         string    `firestore:"id"`
	RecordID   string    `firestore:"record_id"`
	Status     string    `firestore:"status"`
	ReceivedAt time.Time `firestore:"received_at"`
}
type Snapshot struct {
	Booking   booking.Booking `firestore:"booking"`
	Phone     string          `firestore:"phone"`
	ChangedAt time.Time       `firestore:"changed_at"`
	APIID     string          `firestore:"api_id"`
	Online    bool            `firestore:"online"`
}
type Contact struct {
	Chat        string    `firestore:"chat"`
	Phone       string    `firestore:"phone"`
	Language    string    `firestore:"language"`
	Channel     Channel   `firestore:"channel"`
	RequestedAt time.Time `firestore:"requested_at"`
	ExpiresAt   time.Time `firestore:"expires_at"`
	Blocked     bool      `firestore:"blocked"`
}
type Notice struct {
	ID          string   `firestore:"id"`
	Snapshot    Snapshot `firestore:"snapshot"`
	Purpose     Purpose  `firestore:"purpose"`
	Fingerprint string   `firestore:"fingerprint"`
}

// Entry is private durable integration state. No provider bodies are retained.
type Entry struct {
	Health              messaging.ChannelHealth `firestore:"health,omitempty"`
	Verification        PhoneChallenge          `firestore:"verification,omitempty"`
	Kind                string                  `firestore:"kind"`
	State               string                  `firestore:"state"`
	Event               Event                   `firestore:"event"`
	Contact             Contact                 `firestore:"contact"`
	Notice              Notice                  `firestore:"notice"`
	Fingerprint         string                  `firestore:"fingerprint"`
	ChangedAt           time.Time               `firestore:"changed_at"`
	LeaseOwner          string                  `firestore:"lease_owner"`
	LeaseUntil          time.Time               `firestore:"lease_until"`
	Order               string                  `firestore:"order"`
	Outcome             string                  `firestore:"outcome"`
	Attempted           []string                `firestore:"attempted"`
	UpdatedAt           time.Time               `firestore:"updated_at"`
	SessionCiphertext   []byte                  `firestore:"session_ciphertext,omitempty"`
	NextLookupAt        time.Time               `firestore:"next_lookup_at,omitempty"`
	ProviderState       string                  `firestore:"provider_state,omitempty"`
	SMSFailureAlertedAt time.Time               `firestore:"sms_failure_alerted_at,omitempty"`
}
type Repository interface {
	TransactNotifications(context.Context, []string, func(map[string]*Entry) error) error
	GetNotification(context.Context, string) (Entry, error)
	PendingNotifications(context.Context) ([]Entry, error)
}
type Scheduler interface {
	ScheduleNotification(context.Context, string, string, time.Time) error
}
type Reader interface {
	ReadNativeBooking(context.Context, string) (Snapshot, error)
}
type OwnedBookings interface {
	FindBooking(context.Context, string) (booking.Booking, error)
}

// A true rejection flag guarantees the provider did not accept the message.
// Timeouts and unknown outcomes must never cause a retry or channel fallback.
type Sender interface {
	SendNotification(context.Context, Target, Notice, string) (rejected bool, err error)
}
type Service struct {
	Health                 *HealthGuard
	Repo                   Repository
	Scheduler              Scheduler
	Reader                 Reader
	Owned                  OwnedBookings
	Sender                 Sender
	ActivatedAt            time.Time
	WhatsAppTemplates      map[string]string // purpose:language -> approved template
	Now                    func() time.Time
	TelegramAccount        bool
	BookingPermissionSince time.Time
	NativeLanguage         string
	SMSReady               bool
	SMSPermissionSince     time.Time
	SMSStatus              func(context.Context, Notice) (string, error)
	SMSFailureAlert        func(context.Context, string, string) error
}

func Key(kind, id string) string { return fmt.Sprintf("%s_%x", kind, sha256.Sum256([]byte(id))) }
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func fingerprint(snap Snapshot) string {
	// Administrative comments, attendance and payment changes are not customer
	// booking changes and must not produce another confirmation.
	b := snap.Booking
	if phone, err := customer.NormalizePhone(snap.Phone); err == nil {
		snap.Phone = phone
	}
	raw, _ := json.Marshal(struct {
		Phone, Staff, Status string
		Services             []string
		Start                time.Time
		Duration             time.Duration
	}{snap.Phone, b.StaffID, string(b.Status), b.ServiceIDs, b.StartsAt.UTC(), b.Duration})
	return Key("version", string(raw))
}
func (s *Service) Ingest(ctx context.Context, e Event) error {
	e.ReceivedAt = s.now()
	key := Key("event", e.ID)
	err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		row := rows[key]
		if row.Kind == "" {
			*row = Entry{Kind: "event", State: "pending", Event: e, UpdatedAt: e.ReceivedAt}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.schedule(ctx, e.ID, "initial")
}
func (s *Service) schedule(ctx context.Context, id, attempt string) error {
	return s.Scheduler.ScheduleNotification(ctx, Key("native", id+":"+attempt), id, s.now().Add(2*time.Second))
}
func (s *Service) Reconcile(ctx context.Context) error {
	entries, err := s.Repo.PendingNotifications(ctx)
	if err != nil {
		return err
	}
	var recoveryErr error
	for _, row := range entries {
		if row.Kind != "event" || row.LeaseUntil.After(s.now()) {
			continue
		}
		if err = s.schedule(ctx, row.Event.ID, s.now().Truncate(5*time.Minute).Format(time.RFC3339)); err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
		}
	}
	// Repair the durable backlog before an independent provider health probe.
	// A Graph outage must not prevent Telegram/SMS work from being scheduled.
	recoveryErr = errors.Join(recoveryErr, s.reconcileSMS(ctx))
	if s.Health != nil {
		recoveryErr = errors.Join(recoveryErr, s.Health.Refresh(ctx))
	}
	return recoveryErr
}
func (s *Service) Deliver(ctx context.Context, id string) error {
	if err := s.resumeFailedWhatsApp(ctx, id); err != nil {
		return err
	}
	key := Key("event", id)
	owner := uuid.NewString()
	var row Entry
	now := s.now()
	err := s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		stored := rows[key]
		row = *stored
		if stored.Kind != "event" || stored.State == "done" {
			return nil
		}
		if stored.LeaseUntil.After(now) {
			return ErrBusy
		}
		stored.LeaseOwner = owner
		stored.LeaseUntil = now.Add(2 * time.Minute)
		row = *stored
		return nil
	})
	if err != nil {
		return err
	}
	if row.Kind != "event" || row.State == "done" {
		return nil
	}
	// Release only work that has not recorded a provider send intent.
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.Repo.TransactNotifications(releaseCtx, []string{key}, func(rows map[string]*Entry) error {
			if rows[key].LeaseOwner == owner {
				rows[key].LeaseUntil = time.Time{}
			}
			return nil
		})
	}()
	if row.State == "pending" {
		snap, readErr := s.Reader.ReadNativeBooking(ctx, row.Event.RecordID)
		if readErr != nil {
			return readErr
		}
		_, ownedErr := s.Owned.FindBooking(ctx, row.Event.RecordID)
		if ownedErr != nil && !errors.Is(ownedErr, booking.ErrNotFound) {
			return ownedErr
		}
		// API-created assistant bookings already have a customer confirmation.
		owned := ownedErr == nil || strings.HasPrefix(snap.APIID, "omnichannel-")
		recordKey := Key("record", row.Event.RecordID)
		fp := fingerprint(snap)
		err = s.Repo.TransactNotifications(ctx, []string{key, recordKey}, func(rows map[string]*Entry) error {
			event, record := rows[key], rows[recordKey]
			if event.LeaseOwner != owner {
				return ErrBusy
			}
			if record.Fingerprint == fp || (!record.ChangedAt.IsZero() && snap.ChangedAt.Before(record.ChangedAt)) {
				event.State = "done"
				event.Outcome = "duplicate_or_stale"
				row = *event
				return nil
			}
			first := record.Kind == ""
			purpose := BookingChanged
			if first {
				purpose = BookingCreated
			}
			if snap.Booking.Status == booking.StatusCancelled {
				purpose = BookingCancelled
			}
			permission := record.Contact
			if first && row.Event.Status == "create" && snap.Online && snap.APIID == "" && !s.BookingPermissionSince.IsZero() && !snap.Booking.CreatedAt.Before(s.BookingPermissionSince) {
				if phone, err := customer.NormalizePhone(snap.Phone); err == nil {
					permission = Contact{Phone: phone, RequestedAt: snap.Booking.CreatedAt}
				}
			}
			*record = Entry{Kind: "record", Fingerprint: fp, ChangedAt: snap.ChangedAt, UpdatedAt: now, Contact: permission}
			event.Notice = Notice{ID: event.Event.ID, Snapshot: snap, Purpose: purpose, Fingerprint: fp}
			event.State = "prepared"
			event.UpdatedAt = now
			// Starting this integration must not notify clients about old appointments.
			if owned || (first && (snap.Booking.CreatedAt.IsZero() || snap.Booking.CreatedAt.Before(s.ActivatedAt))) || (snap.Booking.Status != booking.StatusCancelled && !snap.Booking.StartsAt.After(now)) {
				event.State = "done"
				event.Outcome = "existing_or_assistant_booking"
			}
			row = *event
			return nil
		})
		if err != nil {
			return err
		}
		if row.State == "done" {
			return nil
		}
	}
	// Re-read immediately before sending so a cancellation or reschedule cannot
	// be followed by a stale queued confirmation.
	latest, err := s.Reader.ReadNativeBooking(ctx, row.Event.RecordID)
	if err != nil {
		return err
	}
	if fingerprint(latest) != row.Notice.Fingerprint {
		return s.finish(ctx, key, owner, "superseded")
	}
	if latest.Booking.Status != booking.StatusCancelled && !latest.Booking.StartsAt.After(s.now()) {
		return s.finish(ctx, key, owner, "appointment_started")
	}
	phone, err := customer.NormalizePhone(latest.Phone)
	if err != nil {
		return s.finish(ctx, key, owner, "no_valid_phone")
	}
	tg, err := s.Repo.GetNotification(ctx, Key("telegram_phone", phone))
	if err != nil {
		return err
	}
	wa, err := s.Repo.GetNotification(ctx, Key("whatsapp_phone", phone))
	if err != nil {
		return err
	}
	policyRow, err := s.Repo.GetNotification(ctx, "policy")
	if err != nil {
		return err
	}
	staffPermission, err := s.Repo.GetNotification(ctx, Key("booking_phone", phone))
	if err != nil {
		return err
	}
	telegramLanguage := string(appointmentmessage.ParseLanguage(tg.Contact.Language))
	if tg.Contact.Language == "" {
		language := s.NativeLanguage
		if staffPermission.Contact.Language != "" {
			language = staffPermission.Contact.Language
		}
		telegramLanguage = string(appointmentmessage.ParseLanguage(language))
	}
	whatsappLanguage := string(appointmentmessage.ParseLanguage(wa.Contact.Language))
	// A request printed at native booking submission applies only to new online
	// records after rollout, never imported/API or administrator records.
	permission, err := s.Repo.GetNotification(ctx, Key("record", row.Event.RecordID))
	if err != nil {
		return err
	}
	requested := !permission.Contact.RequestedAt.IsZero() && permission.Contact.Phone == phone && latest.Online && latest.APIID == "" && !s.BookingPermissionSince.IsZero() && !permission.Contact.RequestedAt.Before(s.BookingPermissionSince)
	staffRequested := staffPermission.Contact.Phone == phone && staffPermission.Contact.ExpiresAt.After(now) && !staffPermission.Contact.Blocked
	if staffRequested {
		requested = true
	}
	// The form's original Telegram/WhatsApp request cannot enroll existing
	// bookings into SMS. Only new SMS-disclosed bookings or staff-recorded
	// client requests qualify; a global withdrawal always wins.
	smsRequested := !staffPermission.Contact.Blocked && (staffRequested ||
		(requested && !s.SMSPermissionSince.IsZero() && !permission.Contact.RequestedAt.Before(s.SMSPermissionSince)))
	recipient := Recipient{Phone: phone, TelegramOptedOut: tg.Contact.Blocked, BookingUpdatesRequested: requested}
	if tg.Contact.ExpiresAt.After(now) {
		recipient.TelegramChat = tg.Contact.Chat
		recipient.TelegramPhone = tg.Contact.Phone
	}
	if wa.Contact.ExpiresAt.After(now) {
		recipient.WhatsAppOptedIn = true
		recipient.WhatsAppOptInPhone = wa.Contact.Phone
	}
	if requested && !wa.Contact.Blocked {
		recipient.WhatsAppOptedIn = true
		recipient.WhatsAppOptInPhone = phone
	}
	order := []Channel{Telegram, WhatsApp}
	if policyRow.Order == "whatsapp" {
		order = []Channel{WhatsApp, Telegram}
	}
	template := s.WhatsAppTemplates[string(row.Notice.Purpose)+":"+whatsappLanguage]
	whatsappReady := template != ""
	if whatsappReady && s.Health != nil {
		whatsappReady, err = s.Health.Allows(ctx, template, whatsappLanguage)
		if err != nil {
			return err
		}
	}
	targets, err := Plan(Policy{MessengerOrder: order, TelegramAccount: s.TelegramAccount, Enabled: map[Channel]bool{Telegram: true, WhatsApp: whatsappReady, SMS: s.SMSReady && smsRequested}, Templates: map[Purpose]string{row.Notice.Purpose: template}}, recipient, row.Notice.Purpose)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return s.finish(ctx, key, owner, "no_linked_messenger")
	}
	deliveryKey := Key("delivery", row.Event.RecordID+":"+row.Event.ID)
	for i, target := range targets {
		route := string(target.Channel)
		if target.StudioAccount {
			route = "telegram_account"
		}
		lang := telegramLanguage
		if target.Channel == WhatsApp {
			lang = whatsappLanguage
		}
		send := false
		alreadyRejected := false
		priorOutcome := ""
		err = s.Repo.TransactNotifications(ctx, []string{deliveryKey}, func(rows map[string]*Entry) error {
			attempt := rows[deliveryKey]
			send = false
			alreadyRejected = false
			priorOutcome = attempt.Outcome
			if attempt.State == "sending" || attempt.State == "done" {
				return nil
			}
			for _, channel := range attempt.Attempted {
				if channel == route {
					alreadyRejected = true
					return nil
				}
			}
			attempt.Kind = "delivery"
			attempt.State = "sending"
			attempt.Outcome = "uncertain_" + route
			attempt.UpdatedAt = now
			attempt.Attempted = append(attempt.Attempted, route)
			send = true
			return nil
		})
		if err != nil {
			return err
		}
		if !send {
			if alreadyRejected && i < len(targets)-1 {
				continue
			}
			if priorOutcome == "" {
				priorOutcome = "already_attempted_or_uncertain"
			}
			return s.finish(ctx, key, owner, priorOutcome, deliveryKey)
		}
		// Once this intent is durable, a timeout is ambiguous and is never repeated.
		rejected, sendErr := s.Sender.SendNotification(ctx, target, row.Notice, lang)
		outcome := "accepted_" + route
		if sendErr != nil {
			outcome = "uncertain_" + route
			if rejected {
				outcome = "rejected_" + route
			}
		}
		if err = s.Repo.TransactNotifications(ctx, []string{deliveryKey}, func(rows map[string]*Entry) error {
			rows[deliveryKey].State = "done"
			if sendErr != nil && rejected {
				rows[deliveryKey].State = "retryable_rejection"
			}
			rows[deliveryKey].Outcome = outcome
			return nil
		}); err != nil {
			return err
		}
		if sendErr == nil || !rejected || i == len(targets)-1 {
			return s.finish(ctx, key, owner, outcome, deliveryKey)
		}
	}
	return nil
}

// AllowPhone records a client's request documented by authenticated studio
// staff. It is not exposed to the AI or accepted from a customer-typed number.
func (s *Service) AllowPhone(ctx context.Context, rawPhone, lang string) error {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	if lang != "en" && lang != "ru" && lang != "hy" {
		return errors.New("unsupported notification language")
	}
	keys := []string{Key("booking_phone", phone), Key("telegram_phone", phone), Key("whatsapp_phone", phone)}
	return s.Repo.TransactNotifications(ctx, keys, func(rows map[string]*Entry) error {
		*rows[keys[0]] = Entry{Kind: "staff_permission", Contact: Contact{Phone: phone, Language: lang, RequestedAt: s.now(), ExpiresAt: s.now().Add(180 * 24 * time.Hour)}}
		rows[keys[1]].Contact.Blocked = false
		rows[keys[2]].Contact.Blocked = false
		return nil
	})
}

// BlockPhone applies a withdrawal across every automatic notification route.
func (s *Service) BlockPhone(ctx context.Context, rawPhone string) error {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	keys := []string{Key("booking_phone", phone), Key("telegram_phone", phone), Key("whatsapp_phone", phone)}
	return s.Repo.TransactNotifications(ctx, keys, func(rows map[string]*Entry) error {
		for _, key := range keys {
			row := rows[key]
			row.Kind = "contact"
			row.Contact.Phone = phone
			row.Contact.Blocked = true
			row.Contact.ExpiresAt = time.Time{}
			row.UpdatedAt = s.now()
		}
		return nil
	})
}
func (s *Service) finish(ctx context.Context, key, owner, outcome string, deliveryKeys ...string) error {
	keys := append([]string{key}, deliveryKeys...)
	return s.Repo.TransactNotifications(ctx, keys, func(rows map[string]*Entry) error {
		row := rows[key]
		if row.LeaseOwner != owner {
			return ErrBusy
		}
		row.State = "done"
		row.Outcome = outcome
		row.UpdatedAt = s.now()
		if len(deliveryKeys) > 0 {
			// A receipt can arrive while the provider call is returning. Preserve
			// its confirmed failure even if this worker just recorded acceptance.
			prepareWhatsAppFallback(row, rows[deliveryKeys[0]], s.now())
		}
		return nil
	})
}
func (s *Service) RequestContact(ctx context.Context, chat, lang string) error {
	key := Key("telegram_chat", chat)
	return s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		row := rows[key]
		row.Kind = "contact"
		row.Contact.Chat = chat
		row.Contact.Language = string(appointmentmessage.ParseLanguage(lang))
		row.Contact.RequestedAt = s.now()
		return nil
	})
}
func (s *Service) LinkTelegram(ctx context.Context, chat, rawPhone string) error {
	phone, err := customer.NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	chatKey := Key("telegram_chat", chat)
	old, err := s.Repo.GetNotification(ctx, chatKey)
	if err != nil {
		return err
	}
	keys := []string{chatKey, Key("telegram_phone", phone)}
	if old.Contact.Phone != "" && old.Contact.Phone != phone {
		keys = append(keys, Key("telegram_phone", old.Contact.Phone))
	}
	return s.Repo.TransactNotifications(ctx, keys, func(rows map[string]*Entry) error {
		own, byPhone := rows[chatKey], rows[Key("telegram_phone", phone)]
		if own.Contact.Phone != old.Contact.Phone {
			return errors.New("contact changed; request linking again")
		}
		if own.Contact.Phone == phone && own.Contact.ExpiresAt.After(s.now()) && byPhone.Contact.Chat == chat {
			return nil
		}
		if own.Contact.RequestedAt.IsZero() || s.now().Sub(own.Contact.RequestedAt) > 10*time.Minute {
			return errors.New("contact request expired; use /notifications")
		}
		if byPhone.Contact.Chat != "" && byPhone.Contact.Chat != chat && byPhone.Contact.ExpiresAt.After(s.now()) {
			return errors.New("this phone is already linked; please contact the studio")
		}
		if len(keys) == 3 && rows[keys[2]].Contact.Chat == chat {
			*rows[keys[2]] = Entry{}
		}
		own.Contact.Phone = phone
		own.Contact.Channel = Telegram
		own.Contact.ExpiresAt = s.now().Add(180 * 24 * time.Hour)
		own.Contact.Blocked = false
		own.Contact.RequestedAt = time.Time{}
		*byPhone = *own
		return nil
	})
}
func (s *Service) UnlinkTelegram(ctx context.Context, chat string) error {
	chatKey := Key("telegram_chat", chat)
	old, err := s.Repo.GetNotification(ctx, chatKey)
	if err != nil {
		return err
	}
	if old.Contact.Phone == "" {
		return nil
	}
	phoneKey := Key("telegram_phone", old.Contact.Phone)
	return s.Repo.TransactNotifications(ctx, []string{chatKey, phoneKey}, func(rows map[string]*Entry) error {
		if rows[chatKey].Contact.Phone != old.Contact.Phone {
			return errors.New("contact changed; try again")
		}
		rows[chatKey].Contact.ExpiresAt = time.Time{}
		rows[chatKey].Contact.Blocked = true
		if rows[phoneKey].Contact.Chat == chat {
			rows[phoneKey].Contact.ExpiresAt = time.Time{}
			rows[phoneKey].Contact.Blocked = true
		}
		return nil
	})
}
func (s *Service) LinkWhatsApp(ctx context.Context, phone, lang string, enabled bool) error {
	phone, err := customer.NormalizePhone(phone)
	if err != nil {
		return err
	}
	key := Key("whatsapp_phone", phone)
	return s.Repo.TransactNotifications(ctx, []string{key}, func(rows map[string]*Entry) error {
		if !enabled {
			*rows[key] = Entry{Kind: "contact", Contact: Contact{Phone: phone, Channel: WhatsApp, Blocked: true}}
			return nil
		}
		*rows[key] = Entry{Kind: "contact", Contact: Contact{Phone: phone, Channel: WhatsApp, Language: string(appointmentmessage.ParseLanguage(lang)), ExpiresAt: s.now().Add(180 * 24 * time.Hour)}}
		return nil
	})
}
