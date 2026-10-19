package main

import (
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"strings"
	"testing"
	"time"
)

func TestReviewsRequireExplicitAttendanceAfterTheVisit(t *testing.T) {
	now := time.Now()
	b := booking.Booking{ExternalID: "42", StaffID: "2", StartsAt: now.Add(-2 * time.Hour), Duration: time.Hour, Status: booking.StatusConfirmed, Attended: true}
	for _, mode := range []string{"attended", "waiting", "future", "ongoing", "cancelled", "expired", "no specialist"} {
		c := b
		switch mode {
		case "waiting":
			c.Attended = false
		case "future":
			c.StartsAt = now.Add(time.Hour)
		case "ongoing":
			c.StartsAt = now.Add(-time.Minute)
		case "cancelled":
			c.Status = booking.StatusCancelled
		case "expired":
			c.StartsAt = now.Add(-91 * 24 * time.Hour)
		case "no specialist":
			c.StaffID = ""
		}
		if reviewEligible(c, now) != (mode == "attended") {
			t.Fatal(mode)
		}
	}
	b.ManagementToken = "private-management-token"
	raw, _ := json.Marshal(reviewVisit(b))
	if strings.Contains(string(raw), b.ManagementToken) {
		t.Fatal("review exposed private proof")
	}
}
