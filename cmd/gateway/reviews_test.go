package main

import (
	"bytes"
	"encoding/json"
	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/application/notifications"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReviewCheckNeverAcceptsAnotherVerifiedPhone(t *testing.T) {
	for _, phone := range []string{"+37491123456", "+37499123456"} {
		snap := notifications.Snapshot{Phone: "+37491123456", Booking: booking.Booking{ExternalID: "123", StaffID: "2", StaffName: "Specialist", StartsAt: time.Now().Add(-2 * time.Hour), Duration: time.Hour, Status: booking.StatusConfirmed, Attended: true, ManagementToken: "private-proof"}}
		s := &notifications.Service{Repo: memory.New(), Reader: contextRecordReader{snapshot: snap}}
		raw := []byte(`{"action":"review-check","phone":"` + phone + `","recordId":"123"}`)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest(http.MethodPost, "/internal/booking-context", bytes.NewReader(raw))
		req.Header.Set("X-Emotion-Time", stamp)
		req.Header.Set("X-Emotion-Signature", contextSignature("test-key", stamp, raw))
		rec := httptest.NewRecorder()
		bookingContextHandler(s, "test-key", "https://book.motionconcept.rest/").ServeHTTP(rec, req)
		want := 404
		if phone == snap.Phone {
			want = 200
		}
		if rec.Code != want {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), snap.Phone) || strings.Contains(rec.Body.String(), "private-proof") {
			t.Fatal("private visit identity leaked")
		}
	}
}

func TestReviewsAcceptPastOwnedBookingsWithoutAnArrivalMark(t *testing.T) {
	now := time.Now()
	b := booking.Booking{ExternalID: "42", StaffID: "2", StartsAt: now.Add(-2 * time.Hour), Duration: time.Hour, Status: booking.StatusConfirmed, Attended: true}
	for _, mode := range []string{"attended", "confirmed only", "no show", "future", "ongoing", "cancelled", "expired", "no specialist", "zero specialist", "zero duration"} {
		c := b
		switch mode {
		case "confirmed only":
			c.Attended = false
		case "no show":
			c.NoShow = true
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
		case "zero specialist":
			c.StaffID = "0"
		case "zero duration":
			c.Duration = 0
		}
		if reviewEligible(c, now) != (mode == "attended" || mode == "confirmed only") {
			t.Fatal(mode)
		}
	}
	b.ManagementToken = "private-management-token"
	raw, _ := json.Marshal(reviewVisit(b))
	if strings.Contains(string(raw), b.ManagementToken) {
		t.Fatal("review exposed private proof")
	}
}
