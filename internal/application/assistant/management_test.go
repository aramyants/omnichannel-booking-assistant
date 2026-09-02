package assistant

import (
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func prepareCancellationCall(id string) ai.Response {
	return toolResponse(id, toolPrepareCancel, `{"reference":"998877"}`)
}

func prepareRescheduleCall(id string) ai.Response {
	return toolResponse(id, toolPrepareMove, `{
		"reference":"998877",
		"date":"`+testNow.Add(72*time.Hour).Format(dateLayout)+`",
		"time":"10:00"
	}`)
}

func appointmentAt(dayOffset int) time.Time {
	day := testNow.Add(time.Duration(dayOffset) * 24 * time.Hour)
	return time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.UTC)
}

func seedOwnedBooking(t *testing.T, store *memory.Store) {
	t.Helper()

	_, err := store.FindOrCreateByChannelIdentity(t.Context(), customer.ChannelIdentity{
		ID:             "identity-1",
		CustomerID:     "cust-1",
		Provider:       messaging.ProviderTelegram,
		ExternalUserID: "219847362",
		DisplayName:    "Anna",
		CreatedAt:      testNow,
	}, customer.Customer{ID: "cust-1", Name: "Anna", CreatedAt: testNow, UpdatedAt: testNow})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := store.SaveBooking(t.Context(), booking.Booking{
		ID:              "bk-1",
		ExternalID:      "998877",
		ManagementToken: "private-record-hash",
		CustomerID:      "cust-1",
		ServiceIDs:      []string{"1001"},
		StaffID:         "501",
		StartsAt:        appointmentAt(2),
		Duration:        time.Hour,
		Status:          booking.StatusConfirmed,
		CreatedAt:       testNow,
	}); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
}

func TestPreparingCancellationDoesNotCancel(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("call_1"),
		textResponse("Shall I cancel that appointment?"),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("cancel-prepare")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.cancelled) != 0 {
		t.Error("preparing a cancellation changed the provider appointment")
	}
	if output := resultOf(t, model, 1); !strings.Contains(output, "has not been cancelled yet") {
		t.Errorf("result = %s, want it to state that no change was made", output)
	}

	conv := openConversation(t, store)
	if conv.BookingChange == nil || conv.BookingChange.Kind != booking.ChangeCancel {
		t.Fatalf("stored change = %+v, want a cancellation draft", conv.BookingChange)
	}
	booked, err := store.ListBookings(t.Context(), conv.CustomerID)
	if err != nil || len(booked) != 1 || booked[0].Status != booking.StatusConfirmed {
		t.Errorf("bookings = %+v, %v; preparation changed local state", booked, err)
	}
}

func TestCancellationCannotBePreparedAndConfirmedFromOneCustomerMessage(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("call_1"),
		toolResponse("call_2", toolConfirmCancel, `{}`),
		textResponse("Please confirm after checking the appointment details."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("cancel-one-message")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.cancelled) != 0 {
		t.Error("the model cancelled an appointment before the customer saw the summary")
	}
	if output := resultOf(t, model, 2); !strings.Contains(output, "in a new message") {
		t.Errorf("result = %s, want same-message cancellation refused", output)
	}
	if conv := openConversation(t, store); conv.BookingChange == nil {
		t.Error("the prepared cancellation was lost instead of remaining ready for confirmation")
	}
}

func TestConfirmedCancellationChangesProviderThenLocalState(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("call_1"),
		textResponse("Shall I cancel that appointment?"),
		// Extra fields cannot replace the stored reference.
		toolResponse("call_2", toolConfirmCancel, `{"reference":"someone-elses"}`),
		textResponse("Your appointment is cancelled."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("cancel-confirm")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.cancelled) != 0 {
		t.Fatal("the appointment was cancelled before the customer replied to the summary")
	}
	if err := svc.Handle(t.Context(), incoming("cancel-confirm-yes")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}
	if len(scheduling.cancelled) != 1 {
		t.Fatalf("cancelled %d provider appointments, want 1", len(scheduling.cancelled))
	}
	if got := scheduling.cancelled[0].ExternalID; got != "998877" {
		t.Errorf("cancelled reference = %q, want the prepared appointment", got)
	}
	if scheduling.cancelled[0].ManagementToken != "private-record-hash" {
		t.Error("the provider did not receive the private management token")
	}

	conv := openConversation(t, store)
	booked, err := store.ListBookings(t.Context(), conv.CustomerID)
	if err != nil || len(booked) != 1 {
		t.Fatalf("ListBookings() = %+v, %v", booked, err)
	}
	if booked[0].Status != booking.StatusCancelled {
		t.Errorf("stored status = %q, want cancelled", booked[0].Status)
	}
	if conv.BookingChange != nil {
		t.Error("the cancellation draft survived a successful change")
	}

	for _, i := range []int{1, 3} {
		if strings.Contains(resultOf(t, model, i), "private-record-hash") {
			t.Fatal("the private booking management token was exposed to the model")
		}
	}
}

func TestPreparingRescheduleDoesNotMoveAppointment(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareRescheduleCall("call_1"),
		textResponse("Shall I move it to Thursday at ten?"),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("move-prepare")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.moved) != 0 {
		t.Error("preparing a reschedule changed the provider appointment")
	}
	if len(scheduling.checked) != 1 {
		t.Errorf("availability was checked %d times, want once for the move", len(scheduling.checked))
	}

	conv := openConversation(t, store)
	if conv.BookingChange == nil || conv.BookingChange.Kind != booking.ChangeReschedule {
		t.Fatalf("stored change = %+v, want a reschedule draft", conv.BookingChange)
	}
	booked, _ := store.ListBookings(t.Context(), conv.CustomerID)
	if got := booked[0].StartsAt; !got.Equal(appointmentAt(2)) {
		t.Errorf("appointment moved during preparation: %s", got)
	}
}

func TestConfirmedRescheduleChangesProviderThenLocalState(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareRescheduleCall("call_1"),
		textResponse("Shall I move it to Thursday at ten?"),
		toolResponse("call_2", toolConfirmMove, `{"date":"2030-01-01","time":"23:00"}`),
		textResponse("Your appointment has been moved."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	planner := &stubReminderPlanner{}
	svc.tools.reminders = planner
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("move-confirm")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.moved) != 0 {
		t.Fatal("the appointment moved before the customer replied to the summary")
	}
	if err := svc.Handle(t.Context(), incoming("move-confirm-yes")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}
	if len(scheduling.movedTo) != 1 {
		t.Fatalf("moved %d provider appointments, want 1", len(scheduling.movedTo))
	}
	if len(planner.planned) != 1 || !planner.planned[0].StartsAt.Equal(appointmentAt(3)) {
		t.Errorf("planned reminders = %+v, want the rescheduled appointment", planner.planned)
	}
	want := appointmentAt(3)
	if !scheduling.movedTo[0].Equal(want) {
		t.Errorf("provider move = %s, want prepared time %s", scheduling.movedTo[0], want)
	}

	conv := openConversation(t, store)
	booked, _ := store.ListBookings(t.Context(), conv.CustomerID)
	if len(booked) != 1 || !booked[0].StartsAt.Equal(want) {
		t.Errorf("stored booking = %+v, want moved time", booked)
	}
	if conv.BookingChange != nil {
		t.Error("the reschedule draft survived a successful change")
	}
}

func TestRescheduleSlotTakenLeavesOriginalAppointment(t *testing.T) {
	scheduling := defaultScheduling()
	scheduling.moveErr = booking.ErrSlotUnavailable
	model := &scriptedAI{responses: []ai.Response{
		prepareRescheduleCall("call_1"),
		textResponse("Shall I move it to Thursday at ten?"),
		toolResponse("call_2", toolConfirmMove, `{}`),
		textResponse("That new time was taken; your original appointment is unchanged."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("move-race")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("move-race-yes")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}
	if output := resultOf(t, model, 3); !strings.Contains(output, "original appointment is unchanged") {
		t.Errorf("result = %s", output)
	}
	conv := openConversation(t, store)
	booked, _ := store.ListBookings(t.Context(), conv.CustomerID)
	wantOriginal := appointmentAt(2)
	if len(booked) != 1 || !booked[0].StartsAt.Equal(wantOriginal) {
		t.Errorf("stored booking = %+v, want original time", booked)
	}
	if conv.BookingChange != nil {
		t.Error("a no-longer-available reschedule remained confirmable")
	}
}

func TestUnknownCancellationOutcomeHandsOverWithoutChangingLocalState(t *testing.T) {
	scheduling := defaultScheduling()
	scheduling.cancelErr = booking.ErrOutcomeUnknown
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("call_1"),
		textResponse("Shall I cancel that appointment?"),
		toolResponse("call_2", toolConfirmCancel, `{}`),
		textResponse("I could not confirm that change. A colleague will check."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})
	seedOwnedBooking(t, store)

	if err := svc.Handle(t.Context(), incoming("cancel-unknown")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("cancel-unknown-yes")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}
	if output := resultOf(t, model, 3); !strings.Contains(output, "Do not say it worked") {
		t.Errorf("result = %s, want uncertainty made explicit", output)
	}
	conv := openConversation(t, store)
	if conv.State != conversation.StateHumanRequested {
		t.Errorf("state = %q, want a person to reconcile the change", conv.State)
	}
	booked, _ := store.ListBookings(t.Context(), conv.CustomerID)
	if len(booked) != 1 || booked[0].Status != booking.StatusConfirmed {
		t.Errorf("local booking = %+v, want it unchanged while outcome is unknown", booked)
	}
}

func TestAnotherCustomersReferenceCannotBeManaged(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCancellationCall("call_1"),
		textResponse("I cannot find that appointment."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})

	if err := store.SaveBooking(t.Context(), booking.Booking{
		ID: "other-booking", ExternalID: "998877", CustomerID: "another-customer",
		StartsAt: testNow.Add(48 * time.Hour), Status: booking.StatusConfirmed,
	}); err != nil {
		t.Fatalf("seed another customer's booking: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("wrong-owner")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if output := resultOf(t, model, 1); !strings.Contains(output, "does not exist") {
		t.Errorf("result = %s, want the reference hidden as nonexistent", output)
	}
	if len(scheduling.cancelled) != 0 {
		t.Error("another customer's appointment reached the cancellation adapter")
	}
}
