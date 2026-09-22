package assistant

import (
	"context"
	"errors"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

// ServiceScheduling is implemented by calendars that distinguish a general
// opening from a slot that fits the selected service. Older calendar adapters
// can keep the basic port; Altegio must always use these filtered operations.
type ServiceScheduling interface {
	ListServicesForStaff(context.Context, string) ([]booking.Service, error)
	ListStaffForServices(context.Context, []string) ([]booking.Staff, error)
	AvailableDatesForServices(context.Context, string, []string) ([]time.Time, error)
	AvailableSlotsForServices(context.Context, string, time.Time, []string) ([]booking.Slot, error)
}

func (t *toolset) staffForService(ctx context.Context, serviceID string) ([]booking.Staff, error) {
	if serviceID == "" {
		return t.scheduling.ListStaff(ctx)
	}
	if _, err := t.findService(ctx, serviceID); err != nil {
		return nil, err
	}
	if calendar, ok := t.scheduling.(ServiceScheduling); ok {
		return calendar.ListStaffForServices(ctx, []string{serviceID})
	}
	return t.scheduling.ListStaff(ctx)
}

func (t *toolset) datesForService(ctx context.Context, staffID, serviceID string) ([]time.Time, error) {
	if calendar, ok := t.scheduling.(ServiceScheduling); ok {
		if err := t.requireService(ctx, serviceID); err != nil {
			return nil, err
		}
		return calendar.AvailableDatesForServices(ctx, staffID, []string{serviceID})
	}
	return t.scheduling.AvailableDates(ctx, staffID)
}

func (t *toolset) slotsForService(ctx context.Context, staffID string, day time.Time, serviceID string) ([]booking.Slot, error) {
	if calendar, ok := t.scheduling.(ServiceScheduling); ok {
		if err := t.requireService(ctx, serviceID); err != nil {
			return nil, err
		}
		return calendar.AvailableSlotsForServices(ctx, staffID, day, []string{serviceID})
	}
	return t.scheduling.AvailableSlots(ctx, staffID, day)
}

func (t *toolset) requireService(ctx context.Context, serviceID string) error {
	if serviceID == "" {
		return errors.New("choose a service first and supply its service_id from list_services; general staff availability cannot establish that the selected treatment fits")
	}
	_, err := t.findService(ctx, serviceID)
	return err
}

// serviceForStaff uses the specialist's actual catalogue to establish both
// eligibility and the duration/price, before asking the customer to confirm.
func (t *toolset) serviceForStaff(ctx context.Context, staffID string, service booking.Service) (booking.Service, bool, error) {
	calendar, ok := t.scheduling.(ServiceScheduling)
	if !ok {
		return service, true, nil
	}
	services, err := calendar.ListServicesForStaff(ctx, staffID)
	if err != nil {
		return booking.Service{}, false, err
	}
	for _, candidate := range services {
		if candidate.ID == service.ID {
			return candidate, true, nil
		}
	}
	return service, false, nil
}

// offerQualifiedStaff handles a valid service paired with a specialist who
// does not offer it. This is a recoverable choice, not a provider outage that
// should stop the bot and send the customer to a manager.
func (t *toolset) offerQualifiedStaff(ctx context.Context, s *session, service booking.Service, requested booking.Staff) (string, error) {
	staff, err := t.staffForService(ctx, service.ID)
	if err != nil {
		return "", err
	}
	type specialist struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	qualified := make([]specialist, 0, len(staff))
	names := make([]string, 0, len(staff))
	for _, person := range staff {
		if person.Bookable {
			qualified = append(qualified, specialist{ID: person.ID, Name: person.Name})
			names = append(names, person.Name)
		}
	}
	s.offerAll(names...)
	return encode(map[string]any{
		"prepared":              false,
		"reason":                "specialist_does_not_offer_service",
		"service":               service.Name,
		"service_id":            service.ID,
		"requested_specialist":  requested.Name,
		"qualified_specialists": qualified,
		"instruction": "Briefly explain that the requested specialist does not offer this service. Ask whether the customer wants one of the qualified specialists or a different service. " +
			"Nothing was booked. Do not hand off solely because of this mismatch. Recheck dates and times for the agreed service and specialist before preparing again.",
	})
}
