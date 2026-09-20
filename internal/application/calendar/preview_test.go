package calendar

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Local-only visual QA with synthetic data. No live customer/calendar writes.
func TestPreviewCalendar(t *testing.T) {
	if os.Getenv("CALENDAR_PREVIEW") != "1" {
		t.Skip("opt-in local visual preview")
	}
	b := calendarBooking()
	b.StartsAt = time.Now().In(b.StartsAt.Location()).Add(72 * time.Hour)
	h := New(&testStore{b: b}, Settings{Name: "E-motion Concept", Location: b.StartsAt.Location(), Addresses: map[string]string{"ru": "Мясникян 1/6, Ереван", "en": "Myasnikyan 1/6, Yerevan", "hy": "Մյասնիկյան 1/6, Երևան"}})
	mux := http.NewServeMux()
	mux.Handle("GET /calendar/{reference}", h)
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Log(strings.Replace(Link("https://preview.test", b, "ru"), "https://preview.test", server.URL, 1))
	select {
	case <-t.Context().Done():
	case <-time.After(10 * time.Minute):
	}
}
