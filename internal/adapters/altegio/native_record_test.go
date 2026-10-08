package altegio

import (
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeRecordReadIsPrivateAndBoundToLocation(t *testing.T) {
	for _, tc := range []struct {
		name, company string
		wantErr       bool
	}{{"studio", testCompanyID, false}, {"other branch", "1", true}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/record/"+testCompanyID+"/123" || r.Header.Get("Authorization") != "Bearer "+testPartnerToken+", User "+testUserToken {
					t.Error("private record credentials or scoped URL missing")
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":123,"company_id":` + tc.company + `,"datetime":"2026-10-06T15:00:00+04:00","create_date":"2026-10-05 18:00:00","last_change_date":"2026-10-05 18:01:00","seance_length":3600,"client":{"name":"Test client","phone":"37491123456"},"staff":{"id":1,"name":"Test specialist"},"services":[{"id":2,"title":"Test treatment"}]}}`))
			}))
			defer server.Close()
			loc, _ := time.LoadLocation("Asia/Yerevan")
			snap, err := newTestClient(t, server, WithLocation(loc)).ReadNativeBooking(t.Context(), "123")
			if tc.wantErr {
				if !errors.Is(err, booking.ErrUnavailable) {
					t.Fatal("wrong location accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snap.Booking.CreatedAt.Hour() != 14 || snap.Booking.StartsAt.Hour() != 11 || snap.Booking.Duration != time.Hour {
				t.Fatal("incorrect native record timestamps")
			}
		})
	}
}

func TestNativeRecordInternationalContactIsNormalizedBeforeNotificationRouting(t *testing.T) {
	for _, tc := range []struct {
		phone, want string
	}{
		{"79161234567", "+79161234567"},
		{"12025550123", "+12025550123"},
		{"442079460018", "+442079460018"},
		{"37491123456", "+37491123456"},
		{"+79161234567", "+79161234567"},
		{"redacted", "redacted"},
	} {
		t.Run(tc.phone, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":123,"company_id":` + testCompanyID + `,"datetime":"2026-10-08T16:00:00+04:00","seance_length":6600,"client":{"id":77,"name":"Client","phone":"` + tc.phone + `"},"staff":{"id":1,"name":"Galina"},"services":[{"id":2,"title":"Motion Relax 110"}]}}`))
			}))
			defer server.Close()
			snap, err := newTestClient(t, server).ReadNativeBooking(t.Context(), "123")
			if err != nil || snap.Phone != tc.want {
				t.Fatalf("notification contact=%q want=%q err=%v", snap.Phone, tc.want, err)
			}
		})
	}
}

func TestProviderPhoneAdaptationDoesNotGuessMalformedValues(t *testing.T) {
	for _, raw := range []string{"", "123", "999123456789", "redacted", "7 (916) 123-45-67", "79161234567 ext 2", "+79161234567/+79161234568"} {
		if phone, err := normalizeAltegioPhone(raw); err == nil {
			t.Errorf("accepted malformed provider phone %q as %q", raw, phone)
		}
	}
}
