package altegio

import (
	"testing"
	"time"
)

func TestNativeAttendancePreservesNoShowForReviewEligibility(t *testing.T) {
	c := &Client{companyID: "1389810", location: time.UTC}
	for _, mark := range []int{-1, 0, 1, 2} {
		dto := nativeRecord{ID: 42, CompanyID: 1389810, Datetime: "2026-10-08T17:45:00+04:00", Length: 3600, Attendance: mark}
		dto.Staff.ID = 2
		snapshot, err := c.nativeSnapshot(dto, 42)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Booking.NoShow != (mark == -1) || snapshot.Booking.Attended != (mark == 1) {
			t.Fatalf("native mark %d lost its attendance meaning", mark)
		}
	}
}
