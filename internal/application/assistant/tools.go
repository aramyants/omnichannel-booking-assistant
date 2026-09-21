package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/aramyants/omnichannel-booking-assistant/internal/application/appointmentmessage"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
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
	Plan(ctx context.Context, b booking.Booking, conv conversation.Conversation, language string) error
}

// session is the state one reply is produced against. Tools that change
// something act on this rather than on globals.
type session struct {
	conv              *conversation.Conversation
	customer          customer.Customer
	incomingMessageID string

	// finalReply is set only after a state-changing tool has a definitive
	// customer-facing result that must not be paraphrased by another model call.
	finalReply string
	finalLinks []messaging.Link

	// handoffReason and handoffDetail record why a person was asked for during
	// this exchange, so the notification sent afterwards can say what happened
	// rather than only that something did.
	handoffReason HandoffReason
	handoffDetail string

	// language is what fixed phrases are written in for this exchange.
	language language

	// choices are options a tool offered, to be shown as buttons by channels
	// that have them. They are the tool's own words: a service name, a time, a
	// specialist. Nothing here is translated, because none of it is this
	// system's language to begin with.
	choices []messaging.Choice

	// candidates collects live lookup labels during this turn. A lookup alone
	// never decides which question the final reply is asking.
	candidates []messaging.Choice

	// offering names a set of labels this system does choose the words for.
	// It is kept as an intent rather than as text because the language to
	// write them in is not settled until the reply itself has been written.
	offering offering
}

// offering names a fixed set of buttons.
type offering int

const (
	offerWhatToolsSaid offering = iota
	offerBookingConfirmation
	offerChangeConfirmation
	offerHelp
	offerMenu
	offerNavigation
)

// offer records the options a tool has put in front of the customer.
//
// Calling it with nothing clears the offer, which is what a tool that ends the
// choosing does: the previous question has been answered, and its buttons must
// not follow the answer down the screen.
func (s *session) offer(labels ...string) {
	if len(labels) == 0 {
		s.offering = offerWhatToolsSaid
		s.choices = nil
		s.candidates = nil
		return
	}
	s.candidates = append(s.candidates, choicesOf(labels...)...)
}

// selectChoices admits only labels returned by tools, in the order the reply
// offers them. Phone/name questions use an empty list. Three options keep the
// next action visible on a phone instead of filling it with a stale time grid.
func (s *session) selectChoices(labels []string) {
	s.choices = nil
	allowed := make(map[string]bool, len(s.candidates))
	for _, candidate := range s.candidates {
		allowed[candidate.Label] = true
	}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if !allowed[label] {
			continue
		}
		s.choices = append(s.choices, messaging.Choice{Label: label})
		delete(allowed, label)
		if len(s.choices) == 3 {
			break
		}
	}
}

// offerFixed records a set of buttons whose words this system chooses.
func (s *session) offerFixed(kind offering) {
	s.offering = kind
	s.choices = nil
}

// buttons are the options to send with replyText.
//
// The language comes from the reply itself where the reply says: the model
// writes in the customer's language, so its own words settle the case the
// transcript cannot, where somebody types Armenian in Latin letters and no
// script anywhere gives them away.
func (s *session) buttons(replyText string) []messaging.Choice {
	lang := s.language
	if written := scriptLanguage(replyText); written != "" {
		lang = written
	}

	switch s.offering {
	case offerBookingConfirmation:
		return confirmBookingChoices(lang)
	case offerChangeConfirmation:
		return confirmChangeChoices(lang)
	case offerHelp:
		return helpChoices(lang)
	case offerMenu:
		return menuChoices(lang)
	case offerNavigation:
		return append([]messaging.Choice(nil), s.choices...)
	default:
		if asksForContactDetails(replyText) {
			return nil
		}
		// Tool results from several rounds can all be candidates. A model may
		// accidentally return a valid time from an earlier lookup while asking
		// for the customer's name. Only show buttons whose labels the customer
		// can actually see offered in this reply.
		visible := make([]messaging.Choice, 0, len(s.choices))
		for _, choice := range s.choices {
			if choiceNamedInReply(replyText, choice.Label, s.candidates) {
				visible = append(visible, choice)
			}
		}
		return visible
	}
}

// choiceNamedInReply matches a whole service, person, date or time label.
// A substring match would mistake "Face Motion" for an offer of that service
// when the reply only names "Face Motion Guasha".
func choiceNamedInReply(reply, label string, candidates []messaging.Choice) bool {
	text := []rune(strings.ToLower(reply))
	needle := []rune(strings.ToLower(strings.TrimSpace(label)))
	if len(needle) == 0 || len(needle) > len(text) {
		return false
	}
	for start := 0; start+len(needle) <= len(text); start++ {
		if start > 0 && choiceWordRune(text[start-1]) && choiceWordRune(needle[0]) {
			continue
		}
		if end := start + len(needle); end < len(text) && choiceWordRune(text[end]) && choiceWordRune(needle[len(needle)-1]) {
			continue
		}
		if slices.Equal(text[start:start+len(needle)], needle) {
			// Several catalogue entries can share a prefix. An occurrence of
			// "Face Motion Guasha" does not offer "Face Motion" unless the
			// shorter name appears separately somewhere else in the reply.
			shadowed := false
			for _, candidate := range candidates {
				longer := []rune(strings.ToLower(candidate.Label))
				if len(longer) > len(needle) && start+len(longer) <= len(text) &&
					slices.Equal(longer[:len(needle)], needle) &&
					slices.Equal(text[start:start+len(longer)], longer) {
					shadowed = true
					break
				}
			}
			if shadowed {
				continue
			}
			return true
		}
	}
	return false
}

func choiceWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Contact details are typed, not chosen from a calendar. This guard covers a
// model that includes a previously looked-up time in both its text and choices
// while its actual question asks for a name or phone number.
func asksForContactDetails(reply string) bool {
	text := strings.ToLower(reply)
	for _, phrase := range []string{
		"your name", "name should", "name to book", "book under", "full name",
		"first name", "last name", "phone number", "your phone", "contact number",
		"ваше имя", "имя и фамил", "какое имя", "как вас зовут",
		"номер телефона", "телефон", "вашу фамил", "ваше фамил",
		"ձեր անուն", "անունը", "ազգանուն", "հեռախոսահամար", "հեռախոս",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// Tool names. They are constants because they appear in three places that must
// agree: the schema shown to the model, the dispatch below, and the logs.
const (
	toolListServices   = "list_services"
	toolListCategories = "list_service_categories"
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

// maxSlotsReturned bounds what one tool call hands back.
//
// It covers a whole day at the finest grid a calendar uses, so no free time is
// ever cut off. A limit of 12 once ended a 30-minute grid at mid-afternoon, and
// the model then told customers a specialist had nothing in the evening when the
// evening was free. Only the few times a reply names become buttons, so the
// longer list costs context, not screen space.
const maxSlotsReturned = 96

// buttonDateLayout is how a day is written on a button: digits only, so that it
// needs no language.
const buttonDateLayout = "02.01"

// noArguments is the schema for a tool that takes none. Strict mode requires
// the object to be described even when it is empty.
const noArguments = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`

// toolset runs the capabilities the model is allowed to ask for.
type toolset struct {
	scheduling Scheduling
	bookings   BookingRepository
	customers  CustomerRepository
	reminders  ReminderPlanner
	messages   appointmentmessage.Renderer
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
			Name:        toolListCategories,
			Description: "List the actual service categories and counts. Use this to match a customer's category request in any language to the calendar's exact category name before calling list_services with that category. This returns no unrelated services or prices.",
			Parameters:  json.RawMessage(noArguments),
		},
		{
			Name:        toolListServices,
			Description: "List services and prices ONLY in the requested category. Call list_service_categories if its exact stored name is unknown. Use an empty category only when the customer asks for all services or has not specified a category. Never show the full catalogue for a category-specific question.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"category":{"type":"string","description":"Exact category name from list_service_categories, for example Face Motion. Empty only for a request without a category."}},"required":["category"],"additionalProperties":false}`),
		},
		{
			Name: toolListStaff,
			Description: "List specialists who can perform the selected service. Call this before " +
				"offering any specialist for a service; being on the general staff list is not enough.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"service_id":{"type":"string","description":"The selected service id from list_services. Empty only when browsing the team before choosing a service."}
				},
				"required":["service_id"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolAvailableDates,
			Description: "List dates when the selected service can be booked with a specialist. " +
				"Choose a service first. Use this when the customer has not named a specific day.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"staff_id":{"type":"string","description":"The id of a specialist returned by list_staff for this service."},
					"service_id":{"type":"string","description":"The selected service id from list_services. Required to find dates that fit this treatment."}
				},
				"required":["staff_id","service_id"],
				"additionalProperties":false
			}`),
		},
		{
			Name: toolAvailableSlots,
			Description: "List free times for the selected service with a specialist on one date. " +
				"These are the only times that may be offered to the customer. " +
				"They are not held: a time can be taken by someone else at any moment.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"staff_id":{"type":"string","description":"The id of the specialist, from list_staff."},
					"service_id":{"type":"string","description":"The selected service id from list_services. Required to find times long enough for this treatment."},
					"date":{"type":"string","description":"The calendar day as YYYY-MM-DD, in the business's own timezone."}
				},
				"required":["staff_id","service_id","date"],
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
			Name: toolListBookings,
			Description: "List this customer's upcoming appointments, soonest first, with the service " +
				"and specialist for each. Cancelled and past appointments are not returned, so what " +
				"this gives back is everything they still have booked.",
			Parameters: json.RawMessage(noArguments),
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
		return t.listServices(ctx, s, call)
	case toolListCategories:
		return t.listCategories(ctx, s)
	case toolListStaff:
		return t.listStaff(ctx, s, call)
	case toolAvailableDates:
		return t.availableDates(ctx, s, call)
	case toolAvailableSlots:
		return t.availableSlots(ctx, s, call)
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
		return t.requestHandoff(s, call)
	default:
		return "", fmt.Errorf("there is no tool called %q", call.Name)
	}
}

func (t *toolset) listServices(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	var args struct {
		Category string `json:"category"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}
	services, err := t.scheduling.ListServices(ctx)
	if err != nil {
		return "", err
	}
	category := strings.TrimSpace(args.Category)
	if category != "" {
		filtered := make([]booking.Service, 0, len(services))
		for _, service := range services {
			if strings.EqualFold(strings.TrimSpace(service.Category), category) {
				filtered = append(filtered, service)
			}
		}
		if len(filtered) == 0 {
			s.offer()
			return encode(map[string]any{
				"services": []any{}, "category": category, "categories": categoriesOf(services),
				"instruction": "No exact category matched. Match the customer's meaning to one of these actual categories, or ask a short clarification. Do not list unrelated services or fall back to the full catalogue.",
			})
		}
		services = filtered
	}

	type item struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Category    string `json:"category,omitempty"`
		Description string `json:"description,omitempty"`

		// Minutes is omitted when the scheduling system has no duration for the
		// service, which is common. Sending a zero would have the assistant
		// telling customers the appointment takes no time at all.
		Minutes int    `json:"minutes,omitempty"`
		Price   string `json:"price,omitempty"`
	}

	items := make([]item, 0, len(services))
	names := make([]string, 0, len(services))
	for _, service := range services {
		items = append(items, item{
			ID:          service.ID,
			Name:        service.Name,
			Category:    service.Category,
			Description: service.Description,
			Minutes:     int(service.Duration.Minutes()),
			Price:       service.PriceLabel(),
		})
		names = append(names, service.Name)
	}

	// Offered as buttons carrying the names exactly as the calendar stores
	// them, so tapping one is the same as typing it and the model is handed a
	// name it can look up rather than a paraphrase of one.
	s.offer()
	s.offer(names...)

	return encode(map[string]any{"services": items, "category": category,
		"instruction": "List only these matching services. Format each as a numbered, scan-friendly block: name, duration and price on one line, then a concise description in the customer's current language when description contains one. Never dump translations in other languages and never invent missing copy. Do not add services from other categories or silently start a booking. List every match in text; buttons may show up to three and the customer may type the number or name of any other service."})
}

func (t *toolset) listStaff(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	var args struct {
		ServiceID string `json:"service_id"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}
	staff, err := t.staffForService(ctx, args.ServiceID)
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
	names := make([]string, 0, len(staff))
	for _, member := range staff {
		items = append(items, item{
			ID:             member.ID,
			Name:           member.Name,
			Specialisation: member.Specialisation,
			Bookable:       member.Bookable,
		})
		// Only the ones who can be booked. A button for somebody who is not
		// taking appointments can only disappoint whoever presses it.
		if member.Bookable {
			names = append(names, member.Name)
		}
	}

	// One specialist is not a choice, and a button asking a customer to pick
	// them reads as a system going through the motions.
	if len(names) > 1 {
		s.offer(names...)
	} else {
		s.offer()
	}

	instruction := "Only these specialists can be offered for the selected service. Their available dates and times may differ. If more than one is bookable, briefly explain that availability depends on the specialist, then ask whose schedule to check. If exactly one is bookable, state whose schedule you are checking and continue directly without asking a one-option question."
	if args.ServiceID == "" {
		instruction = "This is the general team list. Before offering a specialist for a treatment, call list_staff again with the chosen service_id."
	}
	return encode(map[string]any{"specialists": items, "service_id": args.ServiceID, "instruction": instruction})
}

func (t *toolset) availableDates(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	var args struct {
		StaffID   string `json:"staff_id"`
		ServiceID string `json:"service_id"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}
	if args.StaffID == "" {
		return "", errors.New("staff_id is required; call list_staff first")
	}

	dates, err := t.datesForService(ctx, args.StaffID, args.ServiceID)
	if err != nil {
		return "", err
	}

	formatted := make([]string, 0, len(dates))
	labels := make([]string, 0, len(dates))
	for _, day := range dates {
		formatted = append(formatted, day.Format(dateLayout))

		// Day and month in digits. A weekday or a month name would have to be
		// written in the customer's language, and a date is the one thing that
		// reads the same in all of them.
		labels = append(labels, day.Format(buttonDateLayout))
	}

	s.offer(labels...)

	return encode(map[string]any{"dates": formatted, "service_id": args.ServiceID,
		"instruction": "These dates fit the selected service. If none are available, call list_staff for this service to check other qualified specialists; do not offer unfiltered dates."})
}

func (t *toolset) availableSlots(ctx context.Context, s *session, call ai.ToolCall) (string, error) {
	var args struct {
		StaffID   string `json:"staff_id"`
		ServiceID string `json:"service_id"`
		Date      string `json:"date"`
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

	slots, err := t.slotsForService(ctx, args.StaffID, day, args.ServiceID)
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

	// Offered as buttons as well as named in the answer. A time is short enough
	// that they sit three abreast, and it is the one part of this exchange that
	// is genuinely easier to tap than to type.
	s.offer(times...)

	return encode(map[string]any{
		"date":       args.Date,
		"service_id": args.ServiceID,
		"times":      times,
		"note":       "This is every remaining start time for the selected service on this date, earliest first; nothing later exists. They are not reserved. When the customer asks for a part of the day, pick matching times from this list. If none are available, try another date or call list_staff for this service; do not offer unfiltered times.",
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

	// A newly requested selection replaces the old proposal even if it turns
	// out not to fit. An old confirmation must never book the previous service.
	s.conv.Draft = nil
	s.conv.BookingChange = nil
	t.rememberContact(ctx, s, strings.TrimSpace(args.FullName), phone)

	// The service and specialist are looked up rather than taken on trust, so
	// an id the model invented or misremembered is caught here and the summary
	// read back to the customer carries real names.
	service, err := t.findService(ctx, args.ServiceID)
	if err != nil {
		return "", err
	}
	if requiresCoordination(service) {
		return "", errors.New("this treatment requires a colleague: four-hands needs two simultaneous therapists; extra time cannot be booked alone. Offer request_handoff. Do not substitute a standard service")
	}
	staff, err := t.findStaff(ctx, args.StaffID)
	if err != nil {
		return "", err
	}
	if !staff.Bookable {
		return "", fmt.Errorf("%s is not taking appointments at the moment", staff.Name)
	}
	service, qualified, err := t.serviceForStaff(ctx, staff.ID, service)
	if err != nil {
		return "", err
	}
	if !qualified {
		return t.offerQualifiedStaff(ctx, s, service, staff)
	}

	// Use the specialist's service duration when provided, with the filtered
	// slot duration as fallback. The lookup also proves this exact service fits
	// at the requested time rather than trusting a general opening.
	duration, err := t.appointmentLength(ctx, staff.ID, startsAt, service.ID, service.Duration)
	if err != nil {
		return "", err
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
		Duration:              duration,
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

	// The one question in the whole exchange with exactly two answers.
	s.offerFixed(offerBookingConfirmation)

	return encode(map[string]any{
		"prepared":    true,
		"service":     service.Name,
		"specialist":  staff.Name,
		"date":        startsAt.In(t.location).Format(dateLayout),
		"time":        startsAt.In(t.location).Format(timeLayout),
		"minutes":     int(duration.Minutes()),
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
	// Also guard drafts persisted by an older release before coordination rules
	// existed, and services whose live classification changed after preparation.
	for _, serviceID := range draft.ServiceIDs {
		service, err := t.findService(ctx, serviceID)
		if err != nil {
			return "", err
		}
		if requiresCoordination(service) {
			s.conv.Draft = nil
			return "", errors.New("this treatment requires staff coordination; offer request_handoff. Nothing has been booked")
		}
	}

	request := draft.ToRequest(s.customer.ID)
	request.Comment = bookingComment(s.conv.Provider, draft.CustomerName)
	created, err := t.scheduling.Create(ctx, request)

	switch {
	case err == nil:
		// Only now, with a confirmed appointment in hand, may the customer be
		// told they have one.
		s.conv.Draft = nil

		// Preserve the human-readable catalogue snapshot. Provider responses
		// carry identifiers, but a reminder saying only "service 123" is not a
		// useful customer message and resolving a changed catalogue later can be
		// misleading.
		created.CustomerName = draft.CustomerName
		created.ServiceNames = append([]string(nil), draft.ServiceNames...)
		created.StaffName = draft.StaffName

		// Nothing left to ask, so nothing left to tap.
		s.offer()

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
			if planErr := t.reminders.Plan(ctx, created, *s.conv, string(s.language)); planErr != nil {
				t.logger.ErrorContext(ctx, "booked an appointment but could not plan its reminder",
					"error", planErr, "external_id", created.ExternalID)
			}
		}

		messageLanguage := appointmentmessage.ParseLanguage(string(s.language))
		appointment := appointmentmessage.Appointment{
			CalendarURL: func() string {
				if !recorded {
					return ""
				}
				return t.messages.CalendarURL(created, appointmentmessage.ParseLanguage(string(s.language)))
			}(),
			CustomerName: created.CustomerName,
			StartsAt:     created.StartsAt,
			Service:      strings.Join(created.ServiceNames, ", "),
			Specialist:   created.StaffName,
			Reference:    created.ExternalID,
		}
		s.finalReply = t.messages.Confirmation(messageLanguage, appointment)
		s.finalLinks = t.messages.Links(messageLanguage, appointment)

		if recorded {
			t.offerReminderConsent(s)
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

		// The draft is kept rather than cleared: it is the only description of
		// the appointment somebody now has to go and look for in the calendar.
		s.handoffReason = ReasonBookingUnresolved
		s.handoffDetail = fmt.Sprintf(
			"A booking was sent but never confirmed. Check the calendar for %s with %s at %s and tell the customer what happened. Reference to look for: %s",
			strings.Join(draft.ServiceNames, ", "),
			draft.StaffName,
			draft.StartsAt.In(t.location).Format("2 Jan 15:04"),
			draft.IdempotencyKey,
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

// listBookings returns what this customer still has booked here.
//
// Only appointments that are still to come and have not been cancelled. A
// customer asking what they have booked is asking what is coming: answering
// with a visit from March and one they cancelled last week makes them read past
// the answer to find it. It matters more than tidiness, because this list is
// also what the model cancels and moves from, and every past or cancelled entry
// on it is a reference that can only fail once it is used.
//
// The names are what turn "an appointment on Friday at 14:00" into something a
// customer recognises, and without them the assistant cannot say what the
// appointment is even for. They cost one catalogue read between them and are
// best effort: an appointment nobody can name is still an appointment that can
// be cancelled, so a catalogue that will not answer costs the names rather than
// the list.
func (t *toolset) listBookings(ctx context.Context, s *session) (string, error) {
	if t.bookings == nil {
		return "", errors.New("appointment history is not available")
	}

	booked, err := t.bookings.ListBookings(ctx, s.customer.ID)
	if err != nil {
		return "", err
	}

	now := t.now()
	upcoming := make([]booking.Booking, 0, len(booked))
	for _, b := range booked {
		if b.Status == booking.StatusConfirmed && b.StartsAt.After(now) {
			upcoming = append(upcoming, b)
		}
	}

	if len(upcoming) == 0 {
		return encode(map[string]any{
			"appointments": []any{},
			"instruction": "This customer has nothing booked with us. Tell them so plainly " +
				"and offer to book something.",
		})
	}

	serviceNames, staffNames := t.catalogueNames(ctx)

	type item struct {
		Reference string   `json:"reference"`
		Date      string   `json:"date"`
		Time      string   `json:"time"`
		Services  []string `json:"services,omitempty"`
		Staff     string   `json:"staff,omitempty"`
	}

	items := make([]item, 0, len(upcoming))
	for _, b := range upcoming {
		entry := item{
			Reference: b.ExternalID,
			Date:      b.StartsAt.In(t.location).Format(dateLayout),
			Time:      b.StartsAt.In(t.location).Format(timeLayout),
			Staff:     staffNames[b.StaffID],
		}
		for _, serviceID := range b.ServiceIDs {
			if name := serviceNames[serviceID]; name != "" {
				entry.Services = append(entry.Services, name)
			}
		}
		items = append(items, entry)
	}

	return encode(map[string]any{
		"appointments": items,
		"instruction": "These are all of this customer's upcoming appointments, soonest first. " +
			"There are no others. Use a reference exactly as it appears here to cancel or move one.",
	})
}

// catalogueNames reads the catalogue once and returns the names of services and
// specialists, by id.
//
// Deliberately best effort. Its only caller wants names to make a list of
// appointments readable, and a list without them is still true and still
// usable, so a catalogue that cannot be read is logged and leaves the names
// out rather than failing the answer.
func (t *toolset) catalogueNames(ctx context.Context) (services, staff map[string]string) {
	services, staff = map[string]string{}, map[string]string{}

	catalogue, err := t.scheduling.ListServices(ctx)
	if err != nil {
		t.logger.WarnContext(ctx, "could not name the services on a customer's appointments",
			"error", err)
	}
	for _, service := range catalogue {
		services[service.ID] = service.Name
	}

	people, err := t.scheduling.ListStaff(ctx)
	if err != nil {
		t.logger.WarnContext(ctx, "could not name the specialists on a customer's appointments",
			"error", err)
	}
	for _, member := range people {
		staff[member.ID] = member.Name
	}

	return services, staff
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
	return customer.NormalizePhone(raw)
}

// requestHandoff moves the conversation to a colleague.
//
// The state change is what actually stops the assistant replying. Telling the
// model to stop would be a request; changing the state is a rule.
func (t *toolset) requestHandoff(s *session, call ai.ToolCall) (string, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}

	if err := s.conv.TransitionTo(conversation.StateHumanRequested, t.now()); err != nil {
		return "", err
	}

	s.handoffReason = ReasonCustomerAsked
	s.handoffDetail = strings.TrimSpace(args.Reason)

	// A customer waiting for a person is waiting, not choosing.
	s.offer()

	return encode(map[string]any{
		"handed_over": true,
		"instruction": "Tell the customer a colleague will reply shortly. Do not promise a time.",
	})
}

// appointmentLength returns how long the appointment at startsAt runs for.
//
// The slot is the authority. Altegio reports a duration on each offered time
// even when the service itself has none, which is the normal state of a
// business that never filled the field in. The service duration is only a
// fallback for a scheduling system that does the reverse.
func (t *toolset) appointmentLength(
	ctx context.Context,
	staffID string,
	startsAt time.Time,
	serviceID string,
	serviceDuration time.Duration,
) (time.Duration, error) {
	local := startsAt.In(t.location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, t.location)

	slots, err := t.slotsForService(ctx, staffID, day, serviceID)
	if err != nil {
		return 0, err
	}

	for _, slot := range slots {
		if !slot.Start.Equal(startsAt) {
			continue
		}
		if slot.Duration > 0 {
			return slot.Duration, nil
		}
		if serviceDuration > 0 {
			return serviceDuration, nil
		}
		return 0, fmt.Errorf(
			"%w: no appointment length is set for this service or time", booking.ErrRejected)
	}

	// The time is not among those on offer. Either it was never free or it has
	// gone since it was listed, and both mean the customer needs another.
	return 0, fmt.Errorf("%w: %s is not one of the free times on %s",
		booking.ErrSlotUnavailable,
		local.Format(timeLayout),
		local.Format(dateLayout))
}

// rememberContact stores the name and phone a customer gives while booking.
//
// It runs before anything that can fail, so a booking that falls over still
// leaves the business able to call them back. Failure is logged rather than
// returned: losing the contact detail must not lose the booking with it.
func (t *toolset) rememberContact(ctx context.Context, s *session, name, phone string) {
	if t.customers == nil || phone == "" {
		return
	}
	if s.customer.Phone == phone && (name == "" || s.customer.Name == name) {
		return
	}

	if err := t.customers.UpdateContact(ctx, s.customer.ID, name, phone); err != nil {
		t.logger.ErrorContext(ctx, "could not record the contact details a customer gave",
			"error", err, "customer_id", s.customer.ID)
		return
	}

	s.customer.Phone = phone
	if name != "" {
		s.customer.Name = name
	}
}

// bookingComment is the note stored on an appointment the assistant creates.
//
// Calendars such as Altegio match a booking to an existing client card by phone
// number and keep that card's name, so the name a customer gives in the chat can
// silently disappear behind an older one. Writing it on the appointment keeps it
// in front of the colleague who greets them, together with the channel to reply on.
func bookingComment(provider messaging.Provider, customerName string) string {
	channel := channelName(provider)
	if name := strings.TrimSpace(customerName); name != "" {
		return fmt.Sprintf("Booked by the online assistant via %s. Name given in the chat: %s", channel, name)
	}
	return fmt.Sprintf("Booked by the online assistant via %s.", channel)
}

func channelName(provider messaging.Provider) string {
	switch provider {
	case messaging.ProviderTelegram:
		return "Telegram"
	case messaging.ProviderWhatsApp:
		return "WhatsApp"
	case messaging.ProviderMessenger:
		return "Facebook Messenger"
	case messaging.ProviderInstagram:
		return "Instagram"
	case "":
		return "chat"
	default:
		return string(provider)
	}
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
