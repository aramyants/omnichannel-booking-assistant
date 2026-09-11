package altegio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// This mirrors the failure seen in production: Guasha is on the location's
// catalogue, but only one of its two bookable specialists offers it.
func TestServiceFiltersPreventOfferingAnUnqualifiedSpecialist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/book_staff/"):
			if !slices.Equal(r.URL.Query()["service_ids[]"], []string{"13827244"}) {
				t.Errorf("staff lookup omitted selected service: %s", r.URL.RawQuery)
				_, _ = fmt.Fprint(w, `{"success":true,"data":[{"id":3096135,"name":"Armando","bookable":true},{"id":3096082,"name":"Garik","bookable":true}]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":[{"id":3096082,"name":"Garik","bookable":true}]}`)
		case strings.HasPrefix(r.URL.Path, "/book_services/"):
			if r.URL.Query().Get("staff_id") != "3096082" {
				t.Errorf("catalogue was not filtered to the chosen specialist: %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"services":[{"id":13827244,"title":"Face Motion Guasha","active":1,"seance_length":3600,"price_min":29000,"price_max":29000}]}}`)
		case strings.HasPrefix(r.URL.Path, "/book_dates/"):
			if r.URL.Query().Get("staff_id") != "3096082" || !slices.Equal(r.URL.Query()["service_ids[]"], []string{"13827244"}) {
				t.Errorf("dates were not filtered to service and specialist: %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"booking_dates":["2026-09-17"]}}`)
		case strings.HasPrefix(r.URL.Path, "/book_times/"):
			if !slices.Equal(r.URL.Query()["service_ids[]"], []string{"13827244"}) {
				t.Errorf("times lookup omitted selected service: %s", r.URL.RawQuery)
			}
			if strings.Contains(r.URL.Path, "/3096135/") {
				_, _ = fmt.Fprint(w, `{"success":true,"data":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"success":true,"data":[{"time":"13:30","seance_length":3600,"datetime":"2026-09-17T13:30:00+04:00"}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	client := newTestClient(t, srv)
	services := []string{"13827244"}
	staff, err := client.ListStaffForServices(t.Context(), services)
	if err != nil || len(staff) != 1 || staff[0].Name != "Garik" {
		t.Fatalf("qualified specialists = %+v, err = %v; want only Garik", staff, err)
	}
	catalogue, err := client.ListServicesForStaff(t.Context(), staff[0].ID)
	if err != nil || len(catalogue) != 1 || catalogue[0].Duration != time.Hour {
		t.Fatalf("specialist catalogue = %+v, err = %v", catalogue, err)
	}
	dates, err := client.AvailableDatesForServices(t.Context(), staff[0].ID, services)
	if err != nil || len(dates) != 1 {
		t.Fatalf("dates = %v, err = %v", dates, err)
	}
	slots, err := client.AvailableSlotsForServices(t.Context(), staff[0].ID, dates[0], services)
	if err != nil || len(slots) != 1 || slots[0].Duration != time.Hour {
		t.Fatalf("qualified specialist slots = %+v, err = %v", slots, err)
	}
	wrongSlots, err := client.AvailableSlotsForServices(t.Context(), "3096135", dates[0], services)
	if err != nil || len(wrongSlots) != 0 {
		t.Fatalf("unqualified specialist slots = %+v, err = %v; want none", wrongSlots, err)
	}
}

func TestServiceFilterPreservesMultipleServices(t *testing.T) {
	query, err := serviceQuery([]string{"1001", "1002"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(query["service_ids[]"], []string{"1001", "1002"}) {
		t.Fatalf("service query = %v", query)
	}
	if _, err := serviceQuery([]string{"Face Motion Guasha"}); err == nil {
		t.Fatal("a service name was accepted in place of a provider identifier")
	}
}
