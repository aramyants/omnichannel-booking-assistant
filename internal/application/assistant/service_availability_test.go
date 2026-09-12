package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

type serviceCalendar struct {
	*stubScheduling
	staffServices  map[string][]booking.Service
	qualifiedStaff []booking.Staff
	serviceSlots   []booking.Slot
	staffFilters   [][]string
	dateFilters    [][]string
	slotFilters    [][]string
}

func (s *serviceCalendar) ListServicesForStaff(_ context.Context, staffID string) ([]booking.Service, error) {
	return s.staffServices[staffID], nil
}

func (s *serviceCalendar) ListStaffForServices(_ context.Context, ids []string) ([]booking.Staff, error) {
	s.staffFilters = append(s.staffFilters, ids)
	return s.qualifiedStaff, nil
}

func (s *serviceCalendar) AvailableDatesForServices(_ context.Context, _ string, ids []string) ([]time.Time, error) {
	s.dateFilters = append(s.dateFilters, ids)
	return []time.Time{bookingStart()}, nil
}

func (s *serviceCalendar) AvailableSlotsForServices(_ context.Context, _ string, _ time.Time, ids []string) ([]booking.Slot, error) {
	s.slotFilters = append(s.slotFilters, ids)
	return s.serviceSlots, nil
}

func filteredCalendar() *serviceCalendar {
	base := defaultScheduling()
	return &serviceCalendar{
		stubScheduling: base,
		staffServices:  map[string][]booking.Service{"501": base.services},
		qualifiedStaff: base.staff[:1],
		serviceSlots:   []booking.Slot{{Start: bookingStart(), Duration: time.Hour, StaffID: "501"}},
	}
}

func TestServiceSelectionFiltersStaffDatesAndTimes(t *testing.T) {
	calendar := filteredCalendar()
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("staff", toolListStaff, `{"service_id":"1001"}`),
		toolResponse("dates", toolAvailableDates, `{"staff_id":"501","service_id":"1001"}`),
		toolResponse("times", toolAvailableSlots, `{"staff_id":"501","service_id":"1001","date":"`+bookingDay()+`"}`),
		textResponse("10:00 is available. Would you like that time?"),
	}}
	svc, _ := newAIService(t, model, calendar, &fakeSender{})
	if err := svc.Handle(t.Context(), incoming("filtered-selection")); err != nil {
		t.Fatal(err)
	}
	for name, calls := range map[string][][]string{"staff": calendar.staffFilters, "dates": calendar.dateFilters, "times": calendar.slotFilters} {
		if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != "1001" {
			t.Errorf("%s service filters = %v", name, calls)
		}
	}
	if result := resultOf(t, model, 1); strings.Contains(result, "Nare") {
		t.Errorf("unqualified specialist offered: %s", result)
	}
	if result := resultOf(t, model, 3); strings.Contains(result, "10:30") {
		t.Errorf("unfiltered slot offered: %s", result)
	}
}

func TestServiceAwareAvailabilityRequiresAService(t *testing.T) {
	for _, name := range []string{toolAvailableDates, toolAvailableSlots} {
		t.Run(name, func(t *testing.T) {
			calendar := filteredCalendar()
			args := `{"staff_id":"501","service_id":""}`
			if name == toolAvailableSlots {
				args = `{"staff_id":"501","service_id":"","date":"` + bookingDay() + `"}`
			}
			model := &scriptedAI{responses: []ai.Response{
				toolResponse("availability", name, args),
				textResponse("Which service would you like?"),
			}}
			svc, _ := newAIService(t, model, calendar, &fakeSender{})
			if err := svc.Handle(t.Context(), incoming("no-service")); err != nil {
				t.Fatal(err)
			}
			if output := resultOf(t, model, 1); !strings.Contains(output, "choose a service first") {
				t.Errorf("result = %s", output)
			}
			if len(calendar.dateFilters)+len(calendar.slotFilters) != 0 {
				t.Fatal("asked the calendar for availability without a service")
			}
		})
	}
}

func TestUnqualifiedSpecialistOffersRecoveryWithoutHandoff(t *testing.T) {
	calendar := filteredCalendar()
	calendar.staffServices["501"] = nil
	calendar.qualifiedStaff = []booking.Staff{{ID: "502", Name: "Garik", Bookable: true}}
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("incompatible"),
		textResponse("Mariam does not offer this service. Would you like Garik instead?"),
	}}
	staff := &recordingStaff{}
	svc, store := newAIServiceWithStaff(t, model, calendar, &fakeSender{}, staff)
	conv := openConversation(t, store)
	conv.Draft = &booking.Draft{ServiceIDs: []string{"previous-service"}}
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(t.Context(), incoming("incompatible-service")); err != nil {
		t.Fatal(err)
	}
	output := resultOf(t, model, 1)
	if !strings.Contains(output, "specialist_does_not_offer_service") || !strings.Contains(output, "Garik") {
		t.Errorf("recovery result = %s", output)
	}
	conv = openConversation(t, store)
	if conv.State != conversation.StateAssistantActive || conv.Draft != nil {
		t.Errorf("conversation after mismatch = %+v", conv)
	}
	if len(staff.notices)+len(calendar.checked)+len(calendar.created)+len(calendar.slotFilters) != 0 {
		t.Fatal("incompatible pairing reached booking validation, slot lookup, creation, or staff handoff")
	}
}

func TestPreparationUsesServiceSpecificDurationAndPrice(t *testing.T) {
	calendar := filteredCalendar()
	specific := calendar.services[0]
	specific.Duration = 2 * time.Hour
	specific.PriceMin, specific.PriceMax = 9000, 9000
	calendar.staffServices["501"] = []booking.Service{specific}
	calendar.serviceSlots[0].Duration = 2 * time.Hour
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("service-duration"),
		textResponse("Mariam at 10:00 for 120 minutes, 9000 AMD. Shall I book it?"),
	}}
	svc, store := newAIService(t, model, calendar, &fakeSender{})
	if err := svc.Handle(t.Context(), incoming("specific-duration")); err != nil {
		t.Fatal(err)
	}
	conv := openConversation(t, store)
	if conv.Draft == nil || conv.Draft.Duration != 2*time.Hour {
		t.Fatalf("draft = %+v; want the selected service's two-hour duration", conv.Draft)
	}
	if output := resultOf(t, model, 1); !strings.Contains(output, `"price":"9000 AMD"`) {
		t.Errorf("specialist-specific price missing: %s", output)
	}
	if len(calendar.checked) != 1 || calendar.checked[0].Duration != 2*time.Hour || len(calendar.created) != 0 {
		t.Errorf("checked=%+v, created=%+v", calendar.checked, calendar.created)
	}
}

func TestAServiceThatDoesNotFitLeavesNoDraft(t *testing.T) {
	calendar := filteredCalendar()
	calendar.serviceSlots = nil // Generic calendar still lists the requested time.
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("no-room"),
		textResponse("That time is unavailable for this service. Let's check another time."),
	}}
	svc, store := newAIService(t, model, calendar, &fakeSender{})
	if err := svc.Handle(t.Context(), incoming("service-does-not-fit")); err != nil {
		t.Fatal(err)
	}
	if conv := openConversation(t, store); conv.Draft != nil {
		t.Fatalf("draft accepted for generic-only opening: %+v", conv.Draft)
	}
	if len(calendar.checked)+len(calendar.created) != 0 {
		t.Fatal("a time that does not fit the service reached booking validation or creation")
	}
}
