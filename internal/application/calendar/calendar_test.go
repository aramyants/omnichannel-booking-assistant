package calendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

type testStore struct {
	b   booking.Booking
	err error
}

func (s *testStore) FindBooking(context.Context, string) (booking.Booking, error) { return s.b, s.err }

type testReader struct {
	b   booking.Booking
	err error
}

func (s testReader) ReadBooking(context.Context, booking.Booking) (booking.Booking, error) {
	return s.b, s.err
}
func calendarBooking() booking.Booking {
	return booking.Booking{ID: "3d9cc937-402d-4cc5-8180-aa419fc65162", ExternalID: "123", CustomerName: "Private Name", ManagementToken: "private-management", StartsAt: time.Date(2026, 9, 24, 15, 0, 0, 0, time.FixedZone("Asia/Yerevan", 14400)), Duration: 80 * time.Minute, ServiceNames: []string{"Motion Relax"}, StaffName: "Specialist", Status: booking.StatusConfirmed}
}
func calendarRequest(h http.Handler, link string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("GET /calendar/{reference}", h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, link, nil))
	return rec
}

func TestPrivateCalendarLinksAndExports(t *testing.T) {
	b := calendarBooking()
	store := &testStore{b: b}
	h := New(store, Settings{Name: "Motion Concept", Location: b.StartsAt.Location()})
	h.now = func() time.Time { return b.StartsAt.Add(-time.Hour) }
	link := Link("https://example.test", b, "ru")
	if link == "" || strings.Contains(link, b.ID) || strings.Contains(link, b.ManagementToken) {
		t.Fatalf("unsafe link: %s", link)
	}
	rec := calendarRequest(h, link)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Время для себя") || !strings.Contains(rec.Body.String(), "15:00") {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	for _, private := range []string{b.CustomerName, b.ID, b.ManagementToken} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("page leaked %s", private)
		}
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("private response headers missing")
	}
	exported := calendarRequest(h, link+"&format=ics")
	for _, want := range []string{"DTSTART:20260924T110000Z", "DTEND:20260924T122000Z", "TRIGGER:-PT24H", "BEGIN:VCALENDAR"} {
		if !strings.Contains(exported.Body.String(), want) {
			t.Errorf("ICS missing %s", want)
		}
	}
	if !strings.Contains(exported.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("no attachment")
	}
	invalid, _ := url.Parse(link)
	q := invalid.Query()
	q.Set("key", strings.Repeat("x", 43))
	invalid.RawQuery = q.Encode()
	if got := calendarRequest(h, invalid.String()).Code; got != 404 {
		t.Fatalf("bad key status %d", got)
	}
	store.b.Status = booking.StatusCancelled
	if got := calendarRequest(h, link).Code; got != 410 {
		t.Fatalf("cancelled status %d", got)
	}
	store.b = b
	store.b.StartsAt = b.StartsAt.Add(48 * time.Hour)
	if got := calendarRequest(h, link).Body.String(); !strings.Contains(got, "26.09.2026") {
		t.Fatalf("reschedule stale: %s", got)
	}
	store.err = errors.New("temporary outage")
	if got := calendarRequest(h, link).Code; got != 503 {
		t.Fatalf("outage status %d", got)
	}
}

func TestCalendarRefreshFailsClosed(t *testing.T) {
	b := calendarBooking()
	h := New(&testStore{b: b}, Settings{})
	h.now = func() time.Time { return b.StartsAt.Add(-time.Hour) }
	changed := b
	changed.Status = booking.StatusCancelled
	h.WithReader(testReader{b: changed})
	if got := calendarRequest(h, Link("https://example.test", b, "en")).Code; got != 410 {
		t.Fatalf("external cancellation status %d", got)
	}
	h.WithReader(testReader{err: errors.New("offline")})
	if got := calendarRequest(h, Link("https://example.test", b, "en")).Code; got != 503 {
		t.Fatalf("external outage status %d", got)
	}
}

func TestICSInjectionAndUTF8Folding(t *testing.T) {
	b := calendarBooking()
	body := ICS(b, strings.Repeat("Հայերեն ", 30)+"\r\nBEGIN:VEVENT,;\\", "", "<script>", b.StartsAt)
	if strings.Count(body, "\r\nBEGIN:VEVENT\r\n") != 1 {
		t.Fatal("ICS field injected an event")
	}
	if !utf8.ValidString(body) {
		t.Fatal("invalid UTF-8")
	}
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 75 {
			t.Fatalf("line exceeds 75 octets: %d", len(line))
		}
	}
	for _, invalid := range []string{"", "sequential-123", "00000000-0000-0000-0000-000000000000"} {
		b.ID = invalid
		if Link("https://example.test", b, "en") != "" {
			t.Fatal("non-random ID used as capability")
		}
	}
}
