package altegio

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeSourceZeroFormsAreNotThirdPartyAPI(t *testing.T) {
	for _, raw := range []string{`null`, `""`, `"0"`, `0`, `"external-app"`, `2555`} {
		var dto nativeRecord
		if err := json.Unmarshal([]byte(`{"api_id":`+raw+`}`), &dto); err != nil {
			t.Fatal(err)
		}
		want := ""
		if raw == `"external-app"` {
			want = "external-app"
		}
		if raw == `2555` {
			want = "2555"
		}
		if string(dto.APIID) != want {
			t.Fatal(raw, dto.APIID)
		}
	}
}

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

func TestNativeCalendarDurationExcludesOnlyAnExplicitTechnicalBreak(t *testing.T) {
	for _, pause := range []int64{0, 900, -1, 5700, 6000} {
		dto := nativeRecord{ID: 123, CompanyID: 12345, Datetime: "2026-10-20T16:00:00+04:00", Length: 5700, TechnicalBreak: pause}
		dto.Staff.ID = 1
		client := &Client{companyID: "12345", location: time.UTC}
		snap, err := client.nativeSnapshot(dto, 123)
		want := 95 * time.Minute
		if pause == 900 {
			want = 80 * time.Minute
		}
		if err != nil || snap.Booking.Duration != 95*time.Minute || snap.Booking.CalendarDuration() != want {
			t.Fatalf("pause=%d duration=%v calendar=%v error=%v", pause, snap.Booking.Duration, snap.Booking.CalendarDuration(), err)
		}
	}
}

func TestNativeRecordWithoutTreatmentPreservesAppointmentAndRequiredFacts(t *testing.T) {
	for _, tc := range []struct {
		name, services, datetime string
		length, staff            int
		wantErr                  bool
	}{
		{"empty treatment list", `,"services":[]`, "2026-10-11T15:00:00+04:00", 3600, 1, false},
		{"null treatment list", `,"services":null`, "2026-10-11T15:00:00+04:00", 3600, 1, false},
		{"omitted treatment list", "", "2026-10-11T15:00:00+04:00", 3600, 1, false},
		{"missing time", `,"services":[]`, "", 3600, 1, true},
		{"missing duration", `,"services":[]`, "2026-10-11T15:00:00+04:00", 0, 1, true},
		{"missing specialist", `,"services":[]`, "2026-10-11T15:00:00+04:00", 3600, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"success":true,"data":{"id":123,"company_id":%s,"datetime":%q,"seance_length":%d,"client":{"name":"Test client","phone":"37491123456"},"staff":{"id":%d,"name":"Test specialist"}%s}}`, testCompanyID, tc.datetime, tc.length, tc.staff, tc.services)
			}))
			defer server.Close()
			snap, err := newTestClient(t, server).ReadNativeBooking(t.Context(), "123")
			if tc.wantErr {
				if !errors.Is(err, booking.ErrUnavailable) {
					t.Fatalf("missing required facts accepted: %v", err)
				}
				return
			}
			if err != nil || snap.Booking.Duration != time.Hour || snap.Booking.StaffName != "Test specialist" || snap.Phone != "+37491123456" {
				t.Fatalf("valid appointment without treatment lost: snapshot=%+v error=%v", snap, err)
			}
			if len(snap.Booking.ServiceIDs) != 0 || len(snap.Booking.ServiceNames) != 0 {
				t.Fatal("invented a treatment for an empty calendar field")
			}
		})
	}
}
