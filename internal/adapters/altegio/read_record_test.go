package altegio

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

func TestReadBookingRefreshAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		wantErr    bool
		cancelled  bool
	}{
		{"updated", `{"id":123,"datetime":"2026-09-25T15:00:00+0400","length":4800,"staff":{"id":42,"name":"Therapist"},"services":[{"id":10,"title":"80 min"}]}`, false, false},
		{"cancelled", `{"id":123,"deleted":true}`, false, true},
		{"wrong record", `{"id":124,"deleted":true}`, true, false},
		{"incomplete", `{"id":123,"datetime":"2026-09-25T15:00:00+04:00","length":0}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/book_record/"+testCompanyID+"/123/private-proof" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"success":true,"data":` + tc.data + `}`))
			}))
			defer srv.Close()
			b := booking.Booking{ID: "local-id", ExternalID: "123", ManagementToken: "private-proof", ServiceIDs: []string{"10"}, ServiceNames: []string{"Motion Relax 80 min"}, Status: booking.StatusConfirmed}
			got, err := newTestClient(t, srv).ReadBooking(t.Context(), b)
			if tc.wantErr {
				if !errors.Is(err, booking.ErrUnavailable) {
					t.Fatalf("expected unavailable, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.cancelled {
				if got.Status != booking.StatusCancelled {
					t.Fatal("external cancellation lost")
				}
				return
			}
			if got.Duration != 80*time.Minute || got.StartsAt.UTC().Hour() != 11 || got.StaffID != "42" || got.ServiceNames[0] != b.ServiceNames[0] || got.ID != b.ID || got.ManagementToken != b.ManagementToken {
				t.Fatalf("incorrect refreshed booking: %+v", got)
			}
		})
	}
}

func TestPrivateBookingProofIsRedactedInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"not found"}}`))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).ReadBooking(t.Context(), booking.Booking{ExternalID: "123", ManagementToken: "private-proof"})
	if !errors.Is(err, booking.ErrNotFound) || strings.Contains(err.Error(), "private-proof") {
		t.Fatalf("unsafe error: %v", err)
	}
	for _, path := range []string{"/book_record/123/456/private-proof", "/user/records/456/private-proof"} {
		if strings.Contains((request{path: path}).logPath(), "private-proof") {
			t.Fatal("private proof in log path")
		}
	}
}
