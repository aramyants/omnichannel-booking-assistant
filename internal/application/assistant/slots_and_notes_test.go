package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// TestEveryFreeTimeOfTheDayReachesTheModel: a day's free times are handed over
// whole. Returning only the first dozen once ended a 30-minute grid at 15:30,
// and the model told a customer the specialist had nothing in the evening when
// the whole evening was free.
func TestEveryFreeTimeOfTheDayReachesTheModel(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	scheduling.slots = nil
	const freeTimes = 22 // every half hour from the first slot, into the evening
	for i := range freeTimes {
		scheduling.slots = append(scheduling.slots, booking.Slot{
			Start:    bookingStart().Add(time.Duration(i) * 30 * time.Minute),
			Duration: time.Hour,
			StaffID:  "501",
		})
	}
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolAvailableSlots, `{"staff_id":"501","date":"`+bookingDay()+`"}`),
		textResponse("The latest times are 20:00 and 20:30."),
	}}
	svc, _ := newAIService(t, model, scheduling, sender)

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	var result struct {
		Times []string `json:"times"`
	}
	if err := json.Unmarshal([]byte(resultOf(t, model, 1)), &result); err != nil {
		t.Fatalf("tool output is not JSON: %v", err)
	}
	if len(result.Times) != freeTimes {
		t.Fatalf("times = %d, want all %d free times", len(result.Times), freeTimes)
	}
	last := scheduling.slots[freeTimes-1].Start.In(svc.tools.location).Format("15:04")
	if result.Times[freeTimes-1] != last {
		t.Errorf("last time = %q, want %q", result.Times[freeTimes-1], last)
	}
}

// TestTheAppointmentCarriesTheNameGivenInTheChat: a calendar that matches the
// phone number to an older client card shows that card's name. The name the
// customer gave, and the channel to answer on, must still reach the colleague.
func TestTheAppointmentCarriesTheNameGivenInTheChat(t *testing.T) {
	sender := &fakeSender{}
	scheduling := defaultScheduling()
	model := &scriptedAI{responses: []ai.Response{
		prepareCall("call_1"),
		textResponse("Haircut with Mariam at 10:00. Shall I book it?"),
		toolResponse("call_2", toolConfirmBooking, `{}`),
		textResponse("Booked."),
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

	comment := scheduling.created[0].Comment
	for _, want := range []string{"Anna Petrosyan", "Telegram"} {
		if !strings.Contains(comment, want) {
			t.Errorf("comment = %q, want it to mention %q", comment, want)
		}
	}
}

func TestBookingCommentNamesTheChannel(t *testing.T) {
	cases := []struct {
		provider messaging.Provider
		name     string
		want     string
	}{
		{messaging.ProviderMessenger, " garlax ", "Booked by the online assistant via Facebook Messenger. Name given in the chat: garlax"},
		{messaging.ProviderInstagram, "", "Booked by the online assistant via Instagram."},
		{messaging.ProviderWhatsApp, "Ani", "Booked by the online assistant via WhatsApp. Name given in the chat: Ani"},
	}
	for _, tc := range cases {
		if got := bookingComment(tc.provider, tc.name); got != tc.want {
			t.Errorf("bookingComment(%q, %q) = %q, want %q", tc.provider, tc.name, got, tc.want)
		}
	}
}
