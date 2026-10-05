// Package notifications contains deterministic transactional-message policy.
// A durable outbox worker must execute the returned plan; planning sends nothing.
package notifications

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
)

type Channel string

const (
	Telegram Channel = "telegram"
	WhatsApp Channel = "whatsapp"
	SMS      Channel = "sms"
)

type Purpose string

const (
	BookingCreated   Purpose = "booking_created"
	BookingChanged   Purpose = "booking_changed"
	BookingCancelled Purpose = "booking_cancelled"
	BookingReminder  Purpose = "booking_reminder"
	Verification     Purpose = "verification"
)

// Recipient is a snapshot from trusted contact storage, never model arguments.
// TelegramPhone is populated only after verified phone-to-chat association.
type Recipient struct {
	Phone                   string
	TelegramChat            string
	TelegramPhone           string
	WhatsAppOptedIn         bool
	WhatsAppOptInPhone      string
	TelegramOptedOut        bool
	BookingUpdatesRequested bool
}

type Policy struct {
	// MessengerOrder can be Telegram,WhatsApp or WhatsApp,Telegram. SMS is
	// always last. Empty selects Telegram,WhatsApp.
	MessengerOrder []Channel
	Enabled        map[Channel]bool
	// Templates are approved transactional templates for this purpose and
	// language. Availability must be checked before building this policy.
	Templates       map[Purpose]string
	TelegramAccount bool
}

type Target struct {
	Channel    Channel
	Address    string
	TemplateID string
	// StudioAccount uses the user API; empty preserves the existing bot route.
	StudioAccount bool
}

func Plan(policy Policy, recipient Recipient, purpose Purpose) ([]Target, error) {
	switch purpose {
	case BookingCreated, BookingChanged, BookingCancelled, BookingReminder, Verification:
	default:
		return nil, errors.New("notifications: unsupported purpose")
	}
	phone, err := customer.NormalizePhone(recipient.Phone)
	if err != nil {
		return nil, errors.New("notifications: invalid recipient phone")
	}
	order := policy.MessengerOrder
	if len(order) == 0 {
		order = []Channel{Telegram, WhatsApp}
	}
	seen := map[Channel]bool{}
	for _, channel := range order {
		if (channel != Telegram && channel != WhatsApp) || seen[channel] {
			return nil, fmt.Errorf("notifications: invalid messenger order")
		}
		seen[channel] = true
	}
	var targets []Target
	// Native Altegio phone verification has its own UX and provider contract.
	// Keep its codes on SMS until alternative delivery and the form's delivery
	// hint have both been confirmed. Booking notices follow messenger priority.
	if purpose != Verification {
		for _, channel := range order {
			if !policy.Enabled[channel] {
				continue
			}
			switch channel {
			case Telegram:
				if recipient.TelegramOptedOut {
					continue
				}
				if strings.TrimSpace(recipient.TelegramChat) != "" && recipient.TelegramPhone == phone {
					targets = append(targets, Target{Channel: Telegram, Address: recipient.TelegramChat})
				} else if policy.TelegramAccount && recipient.BookingUpdatesRequested {
					targets = append(targets, Target{Channel: Telegram, Address: phone, StudioAccount: true})
				}
			case WhatsApp:
				template := strings.TrimSpace(policy.Templates[purpose])
				if recipient.WhatsAppOptedIn && recipient.WhatsAppOptInPhone == phone && template != "" {
					targets = append(targets, Target{Channel: WhatsApp, Address: phone, TemplateID: template})
				}
			}
		}
	}
	if policy.Enabled[SMS] {
		targets = append(targets, Target{Channel: SMS, Address: phone})
	}
	return targets, nil
}
