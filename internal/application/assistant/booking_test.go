package assistant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

// bookingStart is the slot bookings are prepared against in these tests:
// comfortably in the future, and one the stub calendar actually offers.
func bookingStart() time.Time {
	day := testNow.Add(48 * time.Hour)
	return time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.UTC)
}

// bookingDay is that slot's calendar day.
func bookingDay() string {
	return bookingStart().Format("2006-01-02")
}

func prepareCall(id string) ai.Response {
	return toolResponse(id, toolPrepareBooking, `{
		"service_id":"1001",
		"staff_id":"501",
		"date":"`+bookingDay()+`",
		"time":"10:00",
		"phone":"+374 11 223344",
		"full_name":"Anna Petrosyan"
	}`)
}

// resultOf returns the tool output the model saw on the given call.
func resultOf(t *testing.T, model *scriptedAI, requestIndex int) string {
	t.Helper()

	if len(model.requests) <= requestIndex {
		t.Fatalf("the model was called %d times, want more than %d", len(model.requests), requestIndex)
	}
	turns := model.requests[requestIndex].Turns
	if len(turns) == 0 {
		t.Fatalf("call %d carried no tool turns", requestIndex)
	}
	last := turns[len(turns)-1]
	if len(last.Results) == 0 {
		t.Fatalf("the last turn carried no results")
	}
	return last.Results[0].Output
}

// TestPreparingDoesNotBook is the whole reason booking is two steps.
func TestPreparingDoesNotBook(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
	}}
	svc, store := newAIService(t, model, scheduling, sender)
	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(scheduling.created) != 0 {
		t.Error("preparing a booking created an appointment")
	}
	if len(scheduling.checked) != 1 {
		t.Errorf("the time was checked %d times, want 1", len(scheduling.checked))
	}

	output := resultOf(t, model, 1)
	if !strings.Contains(output, "Nothing is booked yet") {
		t.Errorf("result = %s, want it to say nothing is booked", output)
	}

	// The draft is kept so confirming does not have to re-read anything.
	conv := openConversation(t, store)
	if conv.Draft == nil {
		t.Fatal("no draft was stored")
	}
	if conv.Draft.Phone != "+37411223344" {
		t.Errorf("phone = %q, want it normalised", conv.Draft.Phone)
	}
	if conv.Draft.StaffName != "Mariam" {
		t.Errorf("staff name = %q, want it resolved from the catalogue", conv.Draft.StaffName)
	}
}

func TestConfirmingBooksTheAppointment(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("Booked. Your reference is 998877."),
	}}
	svc, store := newAIService(t, model, scheduling, sender)
	planner := &stubReminderPlanner{}
	svc.tools.reminders = planner

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.created) != 0 {
		t.Fatal("the appointment was created before the customer replied to the summary")
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	if len(scheduling.created) != 1 {
		t.Fatalf("created %d appointments, want 1", len(scheduling.created))
	}
	if len(planner.planned) != 1 || planner.planned[0].ExternalID != "998877" {
		t.Errorf("planned reminders = %+v, want the confirmed appointment", planner.planned)
	}

	created := scheduling.created[0]
	if created.Phone != "+37411223344" {
		t.Errorf("phone = %q", created.Phone)
	}
	if created.StaffID != "501" {
		t.Errorf("staff = %q", created.StaffID)
	}
	if created.IdempotencyKey == "" {
		t.Error("the booking was sent without an idempotency key")
	}

	output := resultOf(t, model, 3)
	if !strings.Contains(output, "998877") {
		t.Errorf("result = %s, want the reference", output)
	}

	// The draft is spent once the appointment exists.
	if conv := openConversation(t, store); conv.Draft != nil {
		t.Error("the draft survived a successful booking")
	}

	// The appointment is recorded so the customer can be told about it later.
	booked, err := store.ListBookings(t.Context(), created.CustomerID)
	if err != nil {
		t.Fatalf("ListBookings() returned error: %v", err)
	}
	if len(booked) != 1 || booked[0].ExternalID != "998877" {
		t.Errorf("stored bookings = %+v, want the new appointment", booked)
	}
}

// TestBookingWorksWhenTheServiceHasNoDuration is the bug that broke every
// booking on a live account.
//
// Altegio returns a null duration on the service unless the business fills the
// field in, which is the normal state of a real salon. A draft with no duration
// is refused, so every single booking failed at the last step, and the customer
// was told the system could not prepare it.
//
// The length comes from the offered slot instead, which carries one even when
// the service does not.
func TestBookingWorksWhenTheServiceHasNoDuration(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()

	// Exactly what the live account returns: a real service, no duration.
	scheduling.services = []booking.Service{{
		ID: "1001", Name: "Массаж", Category: "Motion Sport 115 min",
		Duration: 0, PriceMin: 39000, PriceMax: 39000, Currency: "AMD",
	}}

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Shall I book it?"),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	output := resultOf(t, model, 1)
	if strings.Contains(output, "error") {
		t.Fatalf("preparing failed for a service with no duration: %s", output)
	}

	conv := openConversation(t, store)
	if conv.Draft == nil {
		t.Fatal("no draft was stored")
	}
	// 90 minutes is what the slot reports, not the service.
	if conv.Draft.Duration != 90*time.Minute {
		t.Errorf("duration = %s, want the slot's 1h30m", conv.Draft.Duration)
	}
}

// TestPreparingRefusesATimeTheCalendarDoesNotOffer: looking the slot up to find
// its length also proves the time is real, catching one the model invented or
// one that has gone since it was listed.
func TestPreparingRefusesATimeTheCalendarDoesNotOffer(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()

	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolPrepareBooking, `{
			"service_id":"1001",
			"staff_id":"501",
			"date":"`+bookingDay()+`",
			"time":"23:30",
			"phone":"+374 11 223344",
			"full_name":"Anna Petrosyan"
		}`),
		textResponse("That time is not free, sorry."),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	// Reported to the model as a slot that has gone, which is the actionable
	// framing: offer the customer what is left.
	if output := resultOf(t, model, 1); !strings.Contains(output, "just been taken") {
		t.Errorf("result = %s, want the time refused", output)
	}
	if conv := openConversation(t, store); conv.Draft != nil {
		t.Error("a draft was stored for a time the calendar does not offer")
	}
}

// TestThePhoneIsKeptEvenWhenBookingFails: the number a customer gives is often
// the only way to reach them, and it was being lost with the failed booking. A
// colleague reading the handover notice then had no way to call back.
func TestThePhoneIsKeptEvenWhenBookingFails(t *testing.T) {
	sender := &fakeSender{}
	staff := &recordingStaff{}
	scheduling := defaultScheduling()
	scheduling.checkErr = booking.ErrSlotUnavailable

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		toolResponse("call_2", toolRequestHandoff, `{"reason":"booking failed"}`),
		textResponse("A colleague will call you."),
	}}
	svc, _ := newAIServiceWithStaff(t, model, scheduling, sender, staff)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(staff.notices) != 1 {
		t.Fatalf("staff were told %d times, want once", len(staff.notices))
	}
	if got := staff.notices[0].Customer.Phone; got != "+37411223344" {
		t.Errorf("phone in the handover notice = %q, want the number the customer gave", got)
	}
}

// TestConfirmingWithoutPreparingIsRefused: the model cannot book in one shot,
// so a customer cannot end up with an appointment they never agreed to.
func TestConfirmingWithoutPreparingIsRefused(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolConfirmBooking, `{}`),
		textResponse("What would you like to book?"),
	}}
	svc, _ := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(scheduling.created) != 0 {
		t.Error("an appointment was created without being prepared")
	}
	if output := resultOf(t, model, 1); !strings.Contains(output, "nothing to confirm") {
		t.Errorf("result = %s, want a refusal", output)
	}
}

// TestBookingCannotBePreparedAndConfirmedFromOneCustomerMessage closes the
// gap a prompt alone cannot: the model must show the summary, then wait for a
// later customer message before it can create anything.
func TestBookingCannotBePreparedAndConfirmedFromOneCustomerMessage(t *testing.T) {
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("Please confirm after checking those details."),
	}}
	svc, store := newAIService(t, model, scheduling, &fakeSender{})

	if err := svc.Handle(t.Context(), incoming("one-message")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(scheduling.created) != 0 {
		t.Error("the model created an appointment before the customer saw the summary")
	}
	if output := resultOf(t, model, 2); !strings.Contains(output, "confirm in a new message") {
		t.Errorf("result = %s, want same-message confirmation refused", output)
	}
	if conv := openConversation(t, store); conv.Draft == nil {
		t.Error("the prepared booking was lost instead of remaining ready for a real confirmation")
	}
}

// TestConfirmingCannotChangeWhatWasAgreed: confirm_booking takes no arguments,
// so the details cannot drift between the customer agreeing and the appointment
// being made.
func TestConfirmingCannotChangeWhatWasAgreed(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		// The model tries to slip different details in at confirmation.
		toolResponse("call_2", toolConfirmBooking, `{"staff_id":"999","time":"18:00"}`),
		textResponse("Done."),
	}}
	svc, _ := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	if len(scheduling.created) != 1 {
		t.Fatalf("created %d appointments, want 1", len(scheduling.created))
	}

	// What was booked is what the customer agreed to, not what arrived with the
	// confirmation. The tool reads the stored draft and ignores its arguments.
	created := scheduling.created[0]
	if created.StaffID != "501" {
		t.Errorf("staff = %q, want the prepared 501 rather than the injected 999", created.StaffID)
	}
	if got := created.StartsAt.In(time.UTC).Format("15:04"); got != "10:00" {
		t.Errorf("time = %q, want the prepared 10:00 rather than the injected 18:00", got)
	}
}

func TestPreparingRejectsBadInput(t *testing.T) {
	tests := map[string]struct {
		args string
		want string
	}{
		"a service that does not exist": {
			args: `{"service_id":"9999","staff_id":"501","date":"` + bookingDay() + `","time":"10:00","phone":"+37411223344","full_name":"Anna"}`,
			want: "no service with id",
		},
		"a specialist who does not exist": {
			args: `{"service_id":"1001","staff_id":"9999","date":"` + bookingDay() + `","time":"10:00","phone":"+37411223344","full_name":"Anna"}`,
			want: "no specialist with id",
		},
		"a specialist who is not taking appointments": {
			args: `{"service_id":"1001","staff_id":"504","date":"` + bookingDay() + `","time":"10:00","phone":"+37411223344","full_name":"Anna"}`,
			want: "not taking appointments",
		},
		"a time that has passed": {
			args: `{"service_id":"1001","staff_id":"501","date":"2020-01-01","time":"10:00","phone":"+37411223344","full_name":"Anna"}`,
			want: "already passed",
		},
		"a phone number that is not one": {
			args: `{"service_id":"1001","staff_id":"501","date":"` + bookingDay() + `","time":"10:00","phone":"ask me later","full_name":"Anna"}`,
			want: "not a phone number",
		},
		"a phone number that is too short": {
			args: `{"service_id":"1001","staff_id":"501","date":"` + bookingDay() + `","time":"10:00","phone":"123","full_name":"Anna"}`,
			want: "not a usable phone number",
		},
		"a time in the wrong format": {
			args: `{"service_id":"1001","staff_id":"501","date":"` + bookingDay() + `","time":"half past ten","phone":"+37411223344","full_name":"Anna"}`,
			want: "use YYYY-MM-DD and HH:MM",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sender := &fakeSender{}
			scheduling := defaultScheduling()
			model := &scriptedAI{responses: []ai.Response{
				toolResponse("call_1", toolPrepareBooking, tt.args),
				textResponse("Let me check that again."),
			}}
			svc, store := newAIService(t, model, scheduling, sender)

			if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
				t.Fatalf("Handle() returned error: %v", err)
			}

			if output := resultOf(t, model, 1); !strings.Contains(output, tt.want) {
				t.Errorf("result = %s, want it to mention %q", output, tt.want)
			}
			if len(scheduling.created) != 0 {
				t.Error("an appointment was created from rejected input")
			}
			if conv := openConversation(t, store); conv.Draft != nil {
				t.Error("a draft was stored from rejected input")
			}
		})
	}
}

// TestPreparingRefusesATimeThatIsGone catches the race that availability cannot
// prevent: the slot was free when it was listed and taken by the time the
// customer chose it.
func TestPreparingRefusesATimeThatIsGone(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	scheduling.checkErr = booking.ErrSlotUnavailable

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Sorry, that has just gone. Here is what is left."),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if output := resultOf(t, model, 1); !strings.Contains(output, "just been taken") {
		t.Errorf("result = %s, want the slot reported as gone", output)
	}
	if conv := openConversation(t, store); conv.Draft != nil {
		t.Error("a draft was stored for a time that is no longer free")
	}
}

// TestSlotTakenDuringConfirmation is the same race one step later, after the
// customer has already agreed.
func TestSlotTakenDuringConfirmation(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	scheduling.createErr = booking.ErrSlotUnavailable

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("Sorry, someone took that while we were talking."),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	output := resultOf(t, model, 3)
	if !strings.Contains(output, `"booked":false`) {
		t.Errorf("result = %s, want booked false", output)
	}
	if !strings.Contains(output, "Do not say the appointment was made") {
		t.Errorf("result = %s, want the model told not to claim a booking", output)
	}
	if conv := openConversation(t, store); conv.Draft != nil {
		t.Error("the draft survived a booking that failed")
	}
}

// TestUnknownOutcomeGoesToAPerson is the case that must never be guessed: the
// request left, the answer never came, and the appointment may exist.
func TestUnknownOutcomeGoesToAPerson(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	scheduling.createErr = booking.ErrOutcomeUnknown

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("I could not confirm that. A colleague will check and come back to you."),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	output := resultOf(t, model, 3)
	if !strings.Contains(output, "not known whether the appointment was made") {
		t.Errorf("result = %s, want the uncertainty stated plainly", output)
	}
	if !strings.Contains(output, "Do not say it worked and do not say it failed") {
		t.Errorf("result = %s, want the model told not to guess", output)
	}

	// A person has to reconcile it against the scheduling system.
	if conv := openConversation(t, store); conv.State != conversation.StateHumanRequested {
		t.Errorf("state = %q, want the conversation handed over", conv.State)
	}
}

// TestRetryingAConfirmationReusesTheIdempotencyKey is what stops a customer
// ending up with two appointments.
func TestRetryingAConfirmationReusesTheIdempotencyKey(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	scheduling.createErr = errors.New("altegio hiccup")

	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		toolResponse("call_3", toolConfirmBooking, `{}`),
		textResponse("I could not complete that."),
	}}
	svc, _ := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	if len(scheduling.created) < 2 {
		t.Fatalf("the booking was attempted %d times, want at least 2", len(scheduling.created))
	}
	first, second := scheduling.created[0], scheduling.created[1]
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Errorf("two attempts used different keys %q and %q, so a retry could create a second appointment",
			first.IdempotencyKey, second.IdempotencyKey)
	}
}

// TestAnExpiredDraftIsRefused: a draft is not a hold, and confirming an hour-old
// one is more likely to fail than succeed.
func TestAnExpiredDraftIsRefused(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolConfirmBooking, `{}`),
		textResponse("Shall we pick a time again?"),
	}}
	svc, store := newAIService(t, model, scheduling, sender)

	// A draft prepared well before now.
	conv := openConversation(t, store)
	conv.Draft = &booking.Draft{
		IdempotencyKey:        "key-1",
		ServiceIDs:            []string{"1001"},
		StaffID:               "501",
		StartsAt:              testNow.Add(48 * time.Hour),
		Duration:              time.Hour,
		Phone:                 "+37411223344",
		PreparedAt:            testNow.Add(-3 * time.Hour),
		PreparedFromMessageID: "an-earlier-message",
	}
	if err := store.Save(t.Context(), conv); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(scheduling.created) != 0 {
		t.Error("an expired draft was booked")
	}
	if output := resultOf(t, model, 1); !strings.Contains(output, "prepared more than") {
		t.Errorf("result = %s, want the draft reported as stale", output)
	}
}

func TestListingBookings(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		toolResponse("call_3", toolListBookings, `{}`),
		textResponse("You have one appointment."),
	}}
	svc, _ := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("confirmation Handle() returned error: %v", err)
	}

	if output := resultOf(t, model, 4); !strings.Contains(output, "998877") {
		t.Errorf("result = %s, want the booked appointment listed", output)
	}
}
