package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

// Scheduling is the calendar the business actually runs on.
//
// The port carries only what the assistant needs. The model never sees it: it
// asks for a named tool, and the code below decides whether that becomes a call
// to this interface.
type Scheduling interface {
	ListServices(ctx context.Context) ([]booking.Service, error)
	ListStaff(ctx context.Context) ([]booking.Staff, error)
	AvailableDates(ctx context.Context, staffID string) ([]time.Time, error)
	AvailableSlots(ctx context.Context, staffID string, day time.Time) ([]booking.Slot, error)

	// Check asks whether a booking would be accepted, without creating it.
	Check(ctx context.Context, selection booking.Selection) error

	// Create books the appointment, returning it only once the scheduling
	// system has confirmed one exists.
	Create(ctx context.Context, req booking.Request) (booking.Booking, error)

	// Cancel and Reschedule change an appointment that already exists. The
	// Booking value carries the provider's private management proof.
	Cancel(ctx context.Context, b booking.Booking) error
	Reschedule(ctx context.Context, b booking.Booking, startsAt time.Time) (booking.Booking, error)
}

// BookingRepository stores the appointments this system has made, so a customer
// can be told about them without asking the scheduling system every time.
type BookingRepository interface {
	SaveBooking(ctx context.Context, b booking.Booking) error
	ListBookings(ctx context.Context, customerID string) ([]booking.Booking, error)
}

// ReminderPlanner schedules a notification for one exact appointment version.
// A reschedule plans a new version; the old reminder then skips itself when it
// sees that the stored start no longer matches.
type ReminderPlanner interface {
	Plan(ctx context.Context, b booking.Booking, conv conversation.Conversation) error
}

// session is the state one reply is produced against. Tools that change
// something act on this rather than on globals.
type session struct {
	conv              *conversation.Conversation
	customer          customer.Customer
	incomingMessageID string
}

// Tool names. They are constants because they appear in three places that must
// agree: the schema shown to the model, the dispatch below, and the logs.
const (
	toolListServices   = "list_services"
	toolListStaff      = "list_staff"
	toolAvailableDates = "find_available_dates"
	toolAvailableSlots = "find_available_slots"
	toolPrepareBooking = "prepare_booking"
	toolConfirmBooking = "confirm_booking"
	toolListBookings   = "list_my_bookings"
	toolPrepareCancel  = "prepare_cancellation"
	toolConfirmCancel  = "confirm_cancellation"
	toolPrepareMove    = "prepare_reschedule"
	toolConfirmMove    = "confirm_reschedule"
	toolRequestHandoff = "request_human_handoff"
)

// timeLayout is the clock time format used with the model.
const timeLayout = "15:04"

// dateLayout is the calendar-day format used with the model. A bare date avoids
// the model having to reason about offsets, which it does badly.
const dateLayout = "2006-01-02"

// maxSlotsReturned bounds what one tool call hands back. A full day of slots is
// more than a customer can read and more context than the answer needs.
const maxSlotsReturned = 12

// noArguments is the schema for a tool that takes none. Strict mode requires
// the object to be described even when it is empty.
const noArguments = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`

// toolset runs the capabilities the model is allowed to ask for.
type toolset struct {
	scheduling Scheduling
	bookings   BookingRepository
	reminders  ReminderPlanner
	now        func() time.Time
	location   *time.Location
	logger     *slog.Logger
}

// definitions describes the tools to the model.
//
// Every schema sets additionalProperties to false and marks all properties
// required, which is what strict mode needs to guarantee the arguments decode.
// The descriptions are written for the model, and vague wording here produces
// wrong calls more reliably than any other single thing.
func (t *toolset) definitions() []ai.Tool {
	if t.scheduling == nil {
		// Without a calendar the only honest capability left is escalation.
		return []ai.Tool{t.handoffDefinition()}
	}

	tools := []ai.Tool{
		{
			Name: toolListServices,
			Description: "List every service the business offers, with its duration and price. " +
				"Call this before naming any service or quoting any price. Never invent either.",
			Parameters: json.RawMessage(noArguments),
		},
		{
			Name: toolListStaff,
			Description: "List the specialists who work here and whether each is currently taking " +
				"appointments. Call this before naming a specialist.",
			Parameters: json.RawMessage(noArguments),
		},
		{
			Name: toolAvailableDates,
			Description: "List the upcoming dates on which a specialist has any free time. " +
				"Use this when the customer has not named a specific day.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"staff_id":{"type":"string","description":"The id of the specialist, from list_staff."}
				},
				"required":["staff_id"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolAvailableSlots,
			Description: "List the free appointment times for a specialist on one date. " +
				"These are the only times that may be offered to the customer. " +
				"They are not held: a time can be taken by someone else at any moment.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"staff_id":{"type":"string","description":"The id of the specialist, from list_staff."},
					"date":{"type":"string","description":"The calendar day as YYYY-MM-DD, in the business's own timezone."}
				},
				"required":["staff_id","date"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolPrepareBooking,
			Description: "Check that an appointment can be made and hold the details for the customer " +
				"to confirm. This does NOT book anything. Call it once you know the service, the " +
				"specialist, the day, the time and the customer's phone number. Then read the summary " +
				"back and ask the customer to confirm.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"service_id":{"type":"string","description":"The id of the service, from list_services."},
					"staff_id":{"type":"string","description":"The id of the specialist, from list_staff."},
					"date":{"type":"string","description":"The day as YYYY-MM-DD."},
					"time":{"type":"string","description":"The start time as HH:MM, exactly as returned by find_available_slots."},
					"phone":{"type":"string","description":"The customer's phone number. Ask for it; do not invent one."},
					"full_name":{"type":"string","description":"The name to book under."}
				},
				"required":["service_id","staff_id","date","time","phone","full_name"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolConfirmBooking,
			Description: "Book the appointment prepared by prepare_booking. Call this ONLY after the " +
				"customer has clearly agreed to the summary you read back. It takes no arguments: " +
				"the details are whatever the customer already agreed to. Only after this succeeds " +
				"may you tell the customer they have an appointment.",
			Parameters: json.RawMessage(noArguments),
		},
		{
			Name:        toolListBookings,
			Description: "List the appointments this customer has booked through this conversation.",
			Parameters:  json.RawMessage(noArguments),
		},
	}

	if t.bookings != nil {
		tools = append(tools, t.managementDefinitions()...)
	}
	return append(tools, t.handoffDefinition())
}

func (t *toolset) managementDefinitions() []ai.Tool {
	return []ai.Tool{
		{
			Name: toolPrepareCancel,
			Description: "Prepare to cancel one of this customer's appointments. This does NOT cancel it. " +
				"Use a reference from list_my_bookings, then read back the appointment and ask for confirmation.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"reference":{"type":"string","description":"The exact appointment reference from list_my_bookings."}},
				"required":["reference"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolConfirmCancel,
			Description: "Cancel the appointment prepared by prepare_cancellation. Call ONLY after the customer " +
				"has clearly agreed. It takes no arguments, so the reference cannot change after agreement.",
			Parameters: json.RawMessage(noArguments),
		},
		{
			Name: toolPrepareMove,
			Description: "Check and prepare a new date and time for one of this customer's appointments. " +
				"This does NOT move it. Use a reference from list_my_bookings and a time returned by find_available_slots, " +
				"then read back the old and new times and ask for confirmation.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"reference":{"type":"string","description":"The exact appointment reference from list_my_bookings."},
					"date":{"type":"string","description":"The new day as YYYY-MM-DD."},
					"time":{"type":"string","description":"The new start time as HH:MM, exactly as returned by find_available_slots."}
				},
				"required":["reference","date","time"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolConfirmMove,
			Description: "Move the appointment prepared by prepare_reschedule. Call ONLY after the customer " +
				"has clearly agreed. It takes no arguments, so the reference and new time cannot change after agreement.",
			Parameters: json.RawMessage(noArguments),
		},
	}
}

func (t *toolset) handoffDefinition() ai.Tool {
	return ai.Tool{
		Name: toolRequestHandoff,
		Description: "Hand the conversation to a colleague. Use this whenever the customer asks " +
			"for a person, is upset, or wants something you cannot do safely. " +
			"After calling this, tell the customer a colleague will reply, and stop.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"reason":{"type":"string","description":"One short sentence on why a person is needed."}
			},
			"required":["reason"],
			"additionalProperties":false
		}`),
	}
}

// execute runs one tool call.
//
// It never returns an error. A tool that fails hands the model an explanation it
// can act on, because the alternative is a customer met with silence when they
// asked about a day the salon happens to be closed. Genuine faults are still
// logged by the caller.
func (t *toolset) execute(ctx context.Context, s *session, call ai.ToolCall) ai.ToolResult {
	output, err := t.run(ctx, s, call)
	if err != nil {
		return ai.ToolResult{CallID: call.ID, Output: toolFailure(err)}
	}
	return ai.ToolResult{CallID: call.ID, Output: output}
}

func (t *toolset) run(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	// Dispatch is an explicit list, not a lookup on whatever the model sent.
	// A name that is not here is refused rather than resolved.
	switch call.Name {
	case toolListServices:
		return t.listServices(ctx)
	case toolListStaff:
		return t.listStaff(ctx)
	case toolAvailableDates:
		return t.availableDates(ctx, call)
	case toolAvailableSlots:
		return t.availableSlots(ctx, call)
	case toolPrepareBooking:
		return t.prepareBooking(ctx, s, call)
	case toolConfirmBooking:
		return t.confirmBooking(ctx, s)
	case toolListBookings:
		return t.listBookings(ctx, s)
	case toolPrepareCancel:
		return t.prepareCancellation(ctx, s, call)
	case toolConfirmCancel:
		return t.confirmCancellation(ctx, s)
	case toolPrepareMove:
		return t.prepareReschedule(ctx, s, call)
	case toolConfirmMove:
		return t.confirmReschedule(ctx, s)
	case toolRequestHandoff:
		return t.requestHandoff(s.conv, call)
	default:
		return "", fmt.Errorf("there is no tool called %q", call.Name)
	}
}

func (t *toolset) listServices(ctx context.Context) (string, error) {
	services, err := t.scheduling.ListServices(ctx)
	if err != nil {
		return "", err
	}

	type item struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Category string `json:"category,omitempty"`

		// Minutes is omitted when the scheduling system has no duration for the
		// service, which is common. Sending a zero would have the assistant
		// telling customers the appointment takes no time at all.
		Minutes int    `json:"minutes,omitempty"`
		Price   string `json:"price,omitempty"`
	}

	items := make([]item, 0, len(services))
	for _, service := range services {
		items = append(items, item{
			ID:       service.ID,
			Name:     service.Name,
			Category: service.Category,
			Minutes:  int(service.Duration.Minutes()),
			Price:    service.PriceLabel(),
		})
	}
	return encode(map[string]any{"services": items})
}

func (t *toolset) listStaff(ctx context.Context) (string, error) {
	staff, err := t.scheduling.ListStaff(ctx)
	if err != nil {
		return "", err
	}

	type item struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Specialisation string `json:"specialisation,omitempty"`
		Bookable       bool   `json:"accepting_appointments"`
	}

	items := make([]item, 0, len(staff))
	for _, member := range staff {
		items = append(items, item{
			ID:             member.ID,
			Name:           member.Name,
			Specialisation: member.Specialisation,
			Bookable:       member.Bookable,
		})
	}
	return encode(map[string]any{"specialists": items})
}

func (t *toolset) availableDates(ctx context.Context, call ai.ToolCall) (string, error) {
	var args struct {
		StaffID string `json:"staff_id"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}
	if args.StaffID == "" {
		return "", errors.New("staff_id is required; call list_staff first")
	}

	dates, err := t.scheduling.AvailableDates(ctx, args.StaffID)
	if err != nil {
		return "", err
	}

	formatted := make([]string, 0, len(dates))
	for _, day := range dates {
		formatted = append(formatted, day.Format(dateLayout))
	}
	return encode(map[string]any{"dates": formatted})
}

func (t *toolset) availableSlots(ctx context.Context, call ai.ToolCall) (string, error) {
	var args struct {
		StaffID string `json:"staff_id"`
		Date    string `json:"date"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}
	if args.StaffID == "" {
		return "", errors.New("staff_id is required; call list_staff first")
	}

	// The date is parsed in the business's timezone, and refused rather than
	// guessed at. A misread date is a customer sent to the salon on the wrong
	// day.
	day, err := time.ParseInLocation(dateLayout, args.Date, t.location)
	if err != nil {
		return "", fmt.Errorf("date %q is not a calendar day in YYYY-MM-DD form", args.Date)
	}

	// A date in the past is always a misunderstanding, most often the model
	// carrying last year forward. It is refused here rather than sent onward.
	if day.Before(t.startOfToday()) {
		return "", fmt.Errorf("%s is in the past; today is %s",
			args.Date, t.startOfToday().Format(dateLayout))
	}

	slots, err := t.scheduling.AvailableSlots(ctx, args.StaffID, day)
	if err != nil {
		return "", err
	}

	times := make([]string, 0, len(slots))
	for _, slot := range slots {
		// Slots already gone by are dropped: offering a customer a time that
		// has passed reads as the assistant not knowing what day it is.
		if slot.Start.Before(t.now()) {
			continue
		}
		if len(times) == maxSlotsReturned {
			break
		}
		times = append(times, slot.Start.In(t.location).Format("15:04"))
	}

	return encode(map[string]any{
		"date":  args.Date,
		"times": times,
		"note":  "These times are not reserved. Another customer can take one at any moment.",
	})
}

// prepareBooking validates a proposed appointment and stores it for the
// customer to confirm.
//
// It deliberately does not book. Splitting agreement from creation is what makes
// "never tell a customer they have an appointment before one exists" something
// the code enforces rather than something the prompt asks for.
func (t *toolset) prepareBooking(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	var args struct {
		ServiceID string `json:"service_id"`
		StaffID   string `json:"staff_id"`
		Date      string `json:"date"`
		Time      string `json:"time"`
		Phone     string `json:"phone"`
		FullName  string `json:"full_name"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}

	startsAt, err := t.parseAppointmentTime(args.Date, args.Time)
	if err != nil {
		return "", err
	}

	phone, err := normalisePhone(args.Phone)
	if err != nil {
		return "", err
	}

	// The service and specialist are looked up rather than taken on trust, so
	// an id the model invented or misremembered is caught here and the summary
	// read back to the customer carries real names.
	service, err := t.findService(ctx, args.ServiceID)
	if err != nil {
		return "", err
	}
	staff, err := t.findStaff(ctx, args.StaffID)
	if err != nil {
		return "", err
	}
	if !staff.Bookable {
		return "", fmt.Errorf("%s is not taking appointments at the moment", staff.Name)
	}

	draft := booking.Draft{
		// Generated once, here. Reusing it on every confirmation attempt is
		// what stops a retry becoming a second appointment.
		IdempotencyKey:        id.New(),
		ServiceIDs:            []string{service.ID},
		ServiceNames:          []string{service.Name},
		StaffID:               staff.ID,
		StaffName:             staff.Name,
		StartsAt:              startsAt,
		Duration:              service.Duration,
		Phone:                 phone,
		CustomerName:          strings.TrimSpace(args.FullName),
		PreparedAt:            t.now(),
		PreparedFromMessageID: s.incomingMessageID,
	}

	if err := draft.Validate(t.now()); err != nil {
		return "", err
	}

	// Asking the scheduling system now means a time that has already gone is
	// caught before the customer is asked to agree to it.
	if err := t.scheduling.Check(ctx, draft.Selection()); err != nil {
		return "", err
	}

	s.conv.Draft = &draft
	s.conv.BookingChange = nil

	return encode(map[string]any{
		"prepared":    true,
		"service":     service.Name,
		"specialist":  staff.Name,
		"date":        startsAt.In(t.location).Format(dateLayout),
		"time":        startsAt.In(t.location).Format(timeLayout),
		"minutes":     int(service.Duration.Minutes()),
		"price":       service.PriceLabel(),
		"phone":       phone,
		"name":        draft.CustomerName,
		"instruction": "Read these details back and ask the customer to confirm. Nothing is booked yet. Do not say it is.",
	})
}

// confirmBooking creates the appointment the customer agreed to.
//
// It takes no arguments on purpose. The details are whatever was prepared, so
// the model cannot quietly change the time or the price between the customer
// agreeing and the appointment being made.
func (t *toolset) confirmBooking(ctx context.Context, s *session) (string, error) {
	draft := s.conv.Draft
	if draft == nil {
		return "", errors.New("there is nothing to confirm; call prepare_booking first")
	}
	if err := draft.Validate(t.now()); err != nil {
		s.conv.Draft = nil
		return "", err
	}
	if draft.PreparedFromMessageID == s.incomingMessageID {
		return "", errors.New("wait for the customer to confirm in a new message after seeing the summary")
	}

	created, err := t.scheduling.Create(ctx, draft.ToRequest(s.customer.ID))

	switch {
	case err == nil:
		// Only now, with a confirmed appointment in hand, may the customer be
		// told they have one.
		s.conv.Draft = nil

		recorded := false
		if t.bookings != nil {
			if saveErr := t.bookings.SaveBooking(ctx, created); saveErr != nil {
				// The appointment exists in the scheduling system, which is the
				// record that matters. Failing here would tell the customer it
				// did not work when it did.
				t.logger.ErrorContext(ctx, "booked an appointment but could not record it locally",
					"error", saveErr, "external_id", created.ExternalID)
			} else {
				recorded = true
			}
		}
		if recorded && t.reminders != nil {
			if planErr := t.reminders.Plan(ctx, created, *s.conv); planErr != nil {
				t.logger.ErrorContext(ctx, "booked an appointment but could not plan its reminder",
					"error", planErr, "external_id", created.ExternalID)
			}
		}

		return encode(map[string]any{
			"booked":     true,
			"reference":  created.ExternalID,
			"service":    strings.Join(draft.ServiceNames, ", "),
			"specialist": draft.StaffName,
			"date":       created.StartsAt.In(t.location).Format(dateLayout),
			"time":       created.StartsAt.In(t.location).Format(timeLayout),
		})

	case errors.Is(err, booking.ErrSlotUnavailable):
		// Somebody took it between preparing and confirming. The draft is no
		// longer valid, and the customer needs different times.
		s.conv.Draft = nil
		return encode(map[string]any{
			"booked": false,
			"reason": "that time was taken while you were confirming",
			"instruction": "Apologise briefly, then call find_available_slots for the same day " +
				"and offer what is left. Do not say the appointment was made.",
		})

	case errors.Is(err, booking.ErrOutcomeUnknown):
		// The request left but the answer never arrived, so the appointment may
		// or may not exist. Guessing either way risks telling the customer
		// something untrue, so a person checks.
		t.logger.ErrorContext(ctx, "a booking outcome is unknown and needs reconciling",
			"error", err,
			"idempotency_key", draft.IdempotencyKey,
			"conversation_id", s.conv.ID,
			"customer_id", s.customer.ID,
			"starts_at", draft.StartsAt.Format(time.RFC3339),
		)

		if handoffErr := s.conv.TransitionTo(conversation.StateHumanRequested, t.now()); handoffErr != nil {
			t.logger.ErrorContext(ctx, "could not hand over an unresolved booking",
				"error", handoffErr, "conversation_id", s.conv.ID)
		}

		return encode(map[string]any{
			"booked": false,
			"reason": "the booking system did not answer, so it is not known whether the appointment was made",
			"instruction": "Tell the customer you could not confirm it and that a colleague will check " +
				"and come back to them. Do not say it worked and do not say it failed.",
		})

	default:
		return "", err
	}
}

// listBookings returns what this customer has booked here.
func (t *toolset) listBookings(ctx context.Context, s *session) (string, error) {
	if t.bookings == nil {
		return "", errors.New("appointment history is not available")
	}

	booked, err := t.bookings.ListBookings(ctx, s.customer.ID)
	if err != nil {
		return "", err
	}

	type item struct {
		Reference string `json:"reference"`
		Date      string `json:"date"`
		Time      string `json:"time"`
		Status    string `json:"status"`
	}

	items := make([]item, 0, len(booked))
	for _, b := range booked {
		items = append(items, item{
			Reference: b.ExternalID,
			Date:      b.StartsAt.In(t.location).Format(dateLayout),
			Time:      b.StartsAt.In(t.location).Format(timeLayout),
			Status:    string(b.Status),
		})
	}
	return encode(map[string]any{"appointments": items})
}

// parseAppointmentTime reads a day and a clock time in the business's timezone.
func (t *toolset) parseAppointmentTime(date, clock string) (time.Time, error) {
	startsAt, err := time.ParseInLocation(
		dateLayout+" "+timeLayout,
		strings.TrimSpace(date)+" "+strings.TrimSpace(clock),
		t.location,
	)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"could not read %q at %q; use YYYY-MM-DD and HH:MM", date, clock)
	}
	if !startsAt.After(t.now()) {
		return time.Time{}, fmt.Errorf("%s at %s has already passed; today is %s",
			date, clock, t.startOfToday().Format(dateLayout))
	}
	return startsAt, nil
}

func (t *toolset) findService(ctx context.Context, serviceID string) (booking.Service, error) {
	services, err := t.scheduling.ListServices(ctx)
	if err != nil {
		return booking.Service{}, err
	}
	for _, service := range services {
		if service.ID == serviceID {
			return service, nil
		}
	}
	return booking.Service{}, fmt.Errorf("there is no service with id %q; call list_services", serviceID)
}

func (t *toolset) findStaff(ctx context.Context, staffID string) (booking.Staff, error) {
	staff, err := t.scheduling.ListStaff(ctx)
	if err != nil {
		return booking.Staff{}, err
	}
	for _, member := range staff {
		if member.ID == staffID {
			return member, nil
		}
	}
	return booking.Staff{}, fmt.Errorf("there is no specialist with id %q; call list_staff", staffID)
}

// normalisePhone keeps only what a phone number can contain and checks it is a
// plausible length.
//
// It is not a validation of whether the number exists. It exists to catch a
// model that filled the field with something that is obviously not a number,
// because the business will use it to reach the customer.
func normalisePhone(raw string) (string, error) {
	var b strings.Builder
	for i, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')':
			// Separators people write are dropped rather than refused.
		default:
			return "", fmt.Errorf("%q is not a phone number; ask the customer for it", raw)
		}
	}

	normalised := b.String()
	digits := strings.TrimPrefix(normalised, "+")
	if len(digits) < 7 || len(digits) > 15 {
		return "", fmt.Errorf("%q is not a usable phone number; ask the customer to repeat it", raw)
	}
	return normalised, nil
}

// requestHandoff moves the conversation to a colleague.
//
// The state change is what actually stops the assistant replying. Telling the
// model to stop would be a request; changing the state is a rule.
func (t *toolset) requestHandoff(conv *conversation.Conversation, call ai.ToolCall) (string, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}

	if err := conv.TransitionTo(conversation.StateHumanRequested, t.now()); err != nil {
		return "", err
	}

	return encode(map[string]any{
		"handed_over": true,
		"instruction": "Tell the customer a colleague will reply shortly. Do not promise a time.",
	})
}

// startOfToday is midnight in the business's timezone.
func (t *toolset) startOfToday() time.Time {
	now := t.now().In(t.location)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, t.location)
}

func encode(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// toolFailure renders an error for the model.
//
// Scheduling failures are described in terms the model can act on, and anything
// unrecognised is reported without detail: an internal error message is not
// something a customer should end up reading.
func toolFailure(err error) string {
	var message string

	switch {
	case errors.Is(err, booking.ErrSlotUnavailable):
		message = "That time has just been taken. Offer the customer the next available times."
	case errors.Is(err, booking.ErrNotFound):
		message = "That does not exist. Check the catalogue again before answering."
	case errors.Is(err, booking.ErrUnavailable):
		message = "The booking system is not responding. Apologise and offer to have a colleague follow up."
	case errors.Is(err, booking.ErrRejected):
		message = err.Error()
	default:
		message = err.Error()
	}

	encoded, encodeErr := encode(map[string]any{"error": message})
	if encodeErr != nil {
		return `{"error":"the tool failed"}`
	}
	return encoded
}
