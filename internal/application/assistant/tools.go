package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
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
}

// Tool names. They are constants because they appear in three places that must
// agree: the schema shown to the model, the dispatch below, and the logs.
const (
	toolListServices   = "list_services"
	toolListStaff      = "list_staff"
	toolAvailableDates = "find_available_dates"
	toolAvailableSlots = "find_available_slots"
	toolRequestHandoff = "request_human_handoff"
)

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
	now        func() time.Time
	location   *time.Location
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

	return []ai.Tool{
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
		t.handoffDefinition(),
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
func (t *toolset) execute(
	ctx context.Context,
	conv *conversation.Conversation,
	call ai.ToolCall,
) ai.ToolResult {
	output, err := t.run(ctx, conv, call)
	if err != nil {
		return ai.ToolResult{CallID: call.ID, Output: toolFailure(err)}
	}
	return ai.ToolResult{CallID: call.ID, Output: output}
}

func (t *toolset) run(
	ctx context.Context,
	conv *conversation.Conversation,
	call ai.ToolCall,
) (string, error) {
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
	case toolRequestHandoff:
		return t.requestHandoff(conv, call)
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
		Minutes  int    `json:"minutes"`
		Price    string `json:"price,omitempty"`
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
