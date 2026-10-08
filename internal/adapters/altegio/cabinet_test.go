package altegio

import (
	"encoding/json"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestVerifiedPhoneHistoryIsExactScopedAndPaginated(t *testing.T) {
	for _, tc := range []struct {
		name                                                   string
		wrongOwner, wrongPhone, wrongCompany, secondPageDenied bool
	}{
		{name: "complete"}, {name: "foreign owner", wrongOwner: true}, {name: "foreign phone", wrongPhone: true}, {name: "foreign location", wrongCompany: true}, {name: "partial access failure", secondPageDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+testPartnerToken+", User "+testUserToken {
					t.Error("privileged credentials missing")
				}
				if r.Method == http.MethodPost && r.URL.Path == "/company/"+testCompanyID+"/clients/search" {
					var search clientSearchRequest
					if json.NewDecoder(r.Body).Decode(&search) != nil || search.Filters[0].State.Value != "+37491123456" {
						t.Error("search did not use exact proven contact")
					}
					_, _ = w.Write([]byte(`{"success":true,"data":[{"id":77,"name":"Client","phone":"37491123456"},{"id":88,"name":"Near match","phone":"37499123456"}]}`))
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/records/"+testCompanyID || r.URL.Query().Get("client_id") != "77" || r.URL.Query().Get("with_deleted") != "1" {
					t.Errorf("wrong history scope %s", r.URL.Path)
				}
				pages++
				if tc.secondPageDenied && pages == 2 {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"success":false}`))
					return
				}
				count := 100
				if pages == 2 {
					count = 2
				}
				records := make([]map[string]any, 0, count)
				for i := 0; i < count; i++ {
					cid := 77
					phone := "37491123456"
					company, _ := strconv.Atoi(testCompanyID)
					if tc.wrongOwner {
						cid = 88
					}
					if tc.wrongPhone {
						phone = "37499123456"
					}
					if tc.wrongCompany {
						company++
					}
					recordID := i + 1
					if pages == 2 {
						recordID = i + 100
					}
					records = append(records, map[string]any{"id": recordID, "company_id": company, "datetime": "2026-10-09T12:00:00+04:00", "seance_length": 3600, "deleted": recordID == 2, "client": map[string]any{"id": cid, "name": "Client", "phone": phone}, "staff": map[string]any{"id": 1, "name": "Garik"}, "services": []map[string]any{{"id": 2, "title": "Back Motion"}}})
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "data": records}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			loc, _ := time.LoadLocation("Asia/Yerevan")
			got, err := newTestClient(t, server, WithLocation(loc)).ListPhoneBookings(t.Context(), "+37491123456", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
			if tc.wrongOwner || tc.wrongPhone || tc.wrongCompany || tc.secondPageDenied {
				if err == nil || len(got) != 0 {
					t.Fatal("foreign or incomplete history was returned")
				}
				return
			}
			if err != nil || len(got) != 101 || pages != 2 {
				t.Fatalf("records=%d pages=%d err=%v", len(got), pages, err)
			}
			if got[1].Status != booking.StatusCancelled {
				t.Fatal("live cancellation lost")
			}
		})
	}
}
func TestUnavailableContactIsNotAnEmptyHistory(t *testing.T) {
	for _, phone := range []any{nil, "redacted"} {
		t.Run("redacted", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"id": 77, "phone": phone}}})
			}))
			defer server.Close()
			got, err := newTestClient(t, server).ListPhoneBookings(t.Context(), "+37491123456", time.Now())
			if !errors.Is(err, booking.ErrUnavailable) || len(got) != 0 {
				t.Fatal("redacted contact reported as no bookings")
			}
		})
	}
}

func TestVerifiedInternationalPhoneHistory(t *testing.T) {
	for _, tc := range []struct {
		name, verified, stored string
		searchPhone            any
	}{
		{"Armenia", "+37491123456", "37491123456", "37491123456"},
		{"Russia", "+79161234567", "79161234567", "79161234567"},
		{"Russia mixed endpoint format", "+79161234567", "79161234567", "+79161234567"},
		{"Russia numeric client search", "+79161234567", "79161234567", int64(79161234567)},
		{"United States", "+12025550123", "12025550123", "12025550123"},
		{"United Kingdom", "+442079460018", "442079460018", "442079460018"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/company/"+testCompanyID+"/clients/search" {
					var search clientSearchRequest
					if json.NewDecoder(r.Body).Decode(&search) != nil || search.Filters[0].State.Value != tc.verified {
						t.Error("lookup lost verified international contact")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"id": 77, "phone": tc.searchPhone}}})
					return
				}
				if r.URL.Path != "/records/"+testCompanyID || r.URL.Query().Get("client_id") != "77" {
					t.Errorf("unexpected lookup scope: %s", r.URL.Path)
				}
				company, _ := strconv.Atoi(testCompanyID)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"id": 123, "company_id": company, "datetime": "2026-10-08T16:00:00+04:00", "seance_length": 6600, "client": map[string]any{"id": 77, "name": "Client", "phone": tc.stored}, "staff": map[string]any{"id": 1, "name": "Galina"}, "services": []map[string]any{{"id": 2, "title": "Motion Relax 110"}}}}})
			}))
			defer server.Close()
			got, err := newTestClient(t, server, WithLocation(yerevan(t))).ListPhoneBookings(t.Context(), tc.verified, time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC))
			if err != nil || len(got) != 1 || got[0].ExternalID != "123" {
				t.Fatalf("international history: bookings=%d err=%v", len(got), err)
			}
		})
	}
}
