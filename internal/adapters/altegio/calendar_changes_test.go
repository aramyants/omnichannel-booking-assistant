package altegio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCalendarChangesAreWindowedScopedAndIncludeCancellations(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.URL.Path != "/locations/"+testCompanyID+"/appointments" || q.Get("include_deleted") != "true" || q.Get("changed_after") == "" || q.Get("changed_before") == "" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"success":true,"data":[{"id":"42"},{"id":43}]}`)
	}))
	defer server.Close()
	now := time.Now()
	ids, err := newTestClient(t, server).ListChangedNativeBookings(t.Context(), now.Add(-time.Hour), now)
	if err != nil || len(ids) != 2 || calls != 1 {
		t.Fatal(ids, err, calls)
	}
}
func TestCalendarRepeatedPageFailsWithoutReportingPartialSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := `{"id":"42"}`
		fmt.Fprintf(w, `{"success":true,"data":[%s]}`, strings.Join([]string{row, row}, ","))
	}))
	defer server.Close()
	now := time.Now()
	if ids, err := newTestClient(t, server).ListChangedNativeBookings(t.Context(), now.Add(-time.Hour), now); err == nil || len(ids) != 0 {
		t.Fatal("repeated IDs accepted")
	}
}
