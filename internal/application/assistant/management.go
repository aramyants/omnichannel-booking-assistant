package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

func (t *toolset) prepareCancellation(
	ctx context.Context,
	s *session,
	call ai.ToolCall,
) (string, error) {
	var args struct {
		Reference string `json:"reference"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}

	b, err := t.ownedBooking(ctx, s.customer.ID, args.Reference)
	if err != nil {
		return "", err
	}
	if b.Status == booking.StatusCancelled {
		s.conv.BookingChange = nil
		return encode(map[string]any{
			"already_cancelled": true,
			"reference":         b.ExternalID,
			"instruction":       "Tell the customer this appointment is already cancelled.",
		})
	}
	if !b.StartsAt.After(t.now()) {
		return "", fmt.Errorf("appointment %s has already started or passed", b.ExternalID)
	}

	s.conv.Draft = nil
	s.conv.BookingChange = &booking.ChangeDraft{
		Kind:                  booking.ChangeCancel,
		Reference:             b.ExternalID,
		PreparedAt:            t.now(),
		PreparedFromMessageID: s.incomingMessageID,
	}

	return encode(map[string]any{
		"prepared":  true,
		"reference": b.ExternalID,
		"date":      b.StartsAt.In(t.location).Format(dateLayout),
		"time":      b.StartsAt.In(t.location).Format(timeLayout),
		"instruction": "Read this appointment back and ask the customer to confirm cancellation. " +
			"It has not been cancelled yet.",
	})
}

func (t *toolset) confirmCancellation(ctx context.Context, s *session) (string, error) {
	draft := s.conv.BookingChange
	if draft == nil {
		return "", errors.New("there is no cancellation to confirm; call prepare_cancellation first")
	}
	if err := draft.Validate(booking.ChangeCancel, t.now()); err != nil {
		return "", err
	}
	if draft.PreparedFromMessageID == s.incomingMessageID {
		return "", errors.New("wait for the customer to confirm cancellation in a new message after seeing the summary")
	}

	b, err := t.ownedBooking(ctx, s.customer.ID, draft.Reference)
	if err != nil {
		return "", err
	}
	if b.Status == booking.StatusCancelled {
		s.conv.BookingChange = nil
		return encode(map[string]any{"cancelled": true, "reference": b.ExternalID})
	}
	if !b.StartsAt.After(t.now()) {
		s.conv.BookingChange = nil
		return "", fmt.Errorf("appointment %s has already started or passed", b.ExternalID)
	}

	if err := t.scheduling.Cancel(ctx, b); err != nil {
		if errors.Is(err, booking.ErrOutcomeUnknown) {
			return t.unknownChange(ctx, s.conv, "cancellation", b, err)
		}
		return "", err
	}

	// Altegio is the authority. Change local state only after it confirms the
	// cancellation; a local write failure must not turn a real cancellation
	// into a message claiming it failed.
	b.Status = booking.StatusCancelled
	s.conv.BookingChange = nil
	t.recordChangedBooking(ctx, b, "cancelled")

	return encode(map[string]any{
		"cancelled": true,
		"reference": b.ExternalID,
		"date":      b.StartsAt.In(t.location).Format(dateLayout),
		"time":      b.StartsAt.In(t.location).Format(timeLayout),
	})
}

func (t *toolset) prepareReschedule(
	ctx context.Context,
	s *session,
	call ai.ToolCall,
) (string, error) {
	var args struct {
		Reference string `json:"reference"`
		Date      string `json:"date"`
		Time      string `json:"time"`
	}
	if err := call.ArgumentsInto(&args); err != nil {
		return "", err
	}

	b, err := t.ownedBooking(ctx, s.customer.ID, args.Reference)
	if err != nil {
		return "", err
	}
	if b.Status != booking.StatusConfirmed {
		return "", fmt.Errorf("appointment %s is %s and cannot be moved", b.ExternalID, b.Status)
	}
	if !b.StartsAt.After(t.now()) {
		return "", fmt.Errorf("appointment %s has already started or passed", b.ExternalID)
	}

	newStart, err := t.parseAppointmentTime(args.Date, args.Time)
	if err != nil {
		return "", err
	}
	if newStart.Equal(b.StartsAt) {
		return "", errors.New("the new appointment time is the same as the current time")
	}

	// Check the provider again even when the model selected a recently listed
	// slot. Availability is only a snapshot and another customer may have taken
	// it while this conversation continued.
	if err := t.scheduling.Check(ctx, booking.Selection{
		ServiceIDs: b.ServiceIDs,
		StaffID:    b.StaffID,
		StartsAt:   newStart,
		Duration:   b.Duration,
	}); err != nil {
		return "", err
	}

	s.conv.Draft = nil
	s.conv.BookingChange = &booking.ChangeDraft{
		Kind:                  booking.ChangeReschedule,
		Reference:             b.ExternalID,
		NewStart:              newStart,
		PreparedAt:            t.now(),
		PreparedFromMessageID: s.incomingMessageID,
	}

	return encode(map[string]any{
		"prepared":  true,
		"reference": b.ExternalID,
		"from": map[string]string{
			"date": b.StartsAt.In(t.location).Format(dateLayout),
			"time": b.StartsAt.In(t.location).Format(timeLayout),
		},
		"to": map[string]string{
			"date": newStart.In(t.location).Format(dateLayout),
			"time": newStart.In(t.location).Format(timeLayout),
		},
		"instruction": "Read the old and new times back and ask the customer to confirm. " +
			"The appointment has not been moved yet.",
	})
}

func (t *toolset) confirmReschedule(ctx context.Context, s *session) (string, error) {
	draft := s.conv.BookingChange
	if draft == nil {
		return "", errors.New("there is no reschedule to confirm; call prepare_reschedule first")
	}
	if err := draft.Validate(booking.ChangeReschedule, t.now()); err != nil {
		return "", err
	}
	if draft.PreparedFromMessageID == s.incomingMessageID {
		return "", errors.New("wait for the customer to confirm the reschedule in a new message after seeing the summary")
	}

	b, err := t.ownedBooking(ctx, s.customer.ID, draft.Reference)
	if err != nil {
		return "", err
	}
	if b.Status != booking.StatusConfirmed {
		s.conv.BookingChange = nil
		return "", fmt.Errorf("appointment %s is %s and cannot be moved", b.ExternalID, b.Status)
	}
	if !b.StartsAt.After(t.now()) {
		s.conv.BookingChange = nil
		return "", fmt.Errorf("appointment %s has already started or passed", b.ExternalID)
	}

	moved, err := t.scheduling.Reschedule(ctx, b, draft.NewStart)
	switch {
	case err == nil:
		s.conv.BookingChange = nil
		if t.recordChangedBooking(ctx, moved, "rescheduled") && t.reminders != nil {
			if planErr := t.reminders.Plan(ctx, moved, *s.conv); planErr != nil {
				t.logger.ErrorContext(ctx, "rescheduled an appointment but could not plan its reminder",
					"error", planErr, "external_id", moved.ExternalID)
			}
		}
		return encode(map[string]any{
			"rescheduled": true,
			"reference":   moved.ExternalID,
			"date":        moved.StartsAt.In(t.location).Format(dateLayout),
			"time":        moved.StartsAt.In(t.location).Format(timeLayout),
		})

	case errors.Is(err, booking.ErrSlotUnavailable):
		s.conv.BookingChange = nil
		return encode(map[string]any{
			"rescheduled": false,
			"reason":      "the new time was taken while the customer was confirming",
			"instruction": "Apologise briefly and offer the remaining times. The original appointment is unchanged.",
		})

	case errors.Is(err, booking.ErrOutcomeUnknown):
		return t.unknownChange(ctx, s.conv, "reschedule", b, err)

	default:
		return "", err
	}
}

// ownedBooking deliberately reads through the customer-scoped repository.
// Supplying another customer's valid reference therefore looks exactly like a
// nonexistent reference and cannot disclose or mutate their appointment.
func (t *toolset) ownedBooking(
	ctx context.Context,
	customerID string,
	reference string,
) (booking.Booking, error) {
	if t.bookings == nil {
		return booking.Booking{}, errors.New("appointment history is not available")
	}

	reference = strings.TrimSpace(reference)
	booked, err := t.bookings.ListBookings(ctx, customerID)
	if err != nil {
		return booking.Booking{}, err
	}
	for _, b := range booked {
		if b.ExternalID == reference {
			return b, nil
		}
	}
	return booking.Booking{}, fmt.Errorf("%w: no appointment with reference %q", booking.ErrNotFound, reference)
}

func (t *toolset) recordChangedBooking(ctx context.Context, b booking.Booking, action string) bool {
	if err := t.bookings.SaveBooking(ctx, b); err != nil {
		t.logger.ErrorContext(ctx, "appointment changed but could not be updated locally",
			"error", err, "action", action, "external_id", b.ExternalID)
		return false
	}
	return true
}

func (t *toolset) unknownChange(
	ctx context.Context,
	conv *conversation.Conversation,
	action string,
	b booking.Booking,
	cause error,
) (string, error) {
	t.logger.ErrorContext(ctx, "an appointment change outcome is unknown and needs reconciling",
		"error", cause,
		"action", action,
		"external_id", b.ExternalID,
		"conversation_id", conv.ID,
		"customer_id", b.CustomerID,
	)
	if err := conv.TransitionTo(conversation.StateHumanRequested, t.now()); err != nil {
		t.logger.ErrorContext(ctx, "could not hand over an unresolved appointment change",
			"error", err, "conversation_id", conv.ID)
	}

	return encode(map[string]any{
		"changed": false,
		"reason":  "the booking system did not answer, so the result is unknown",
		"instruction": "Tell the customer you could not confirm the change and that a colleague will check. " +
			"Do not say it worked and do not say it failed.",
	})
}
