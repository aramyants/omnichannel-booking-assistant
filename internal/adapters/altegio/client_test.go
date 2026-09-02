package altegio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	// The distroless runtime image carries no timezone database, and neither
	// does a bare Windows toolchain. Embedding it keeps these tests honest
	// about the location handling that production depends on.
	_ "time/tzdata"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
)

const (
	testPartnerToken = "partner-token-value"
	testUserToken    = "user-token-value"
	testCompanyID    = "733419"
)

func yerevan(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation("Asia/Yerevan")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

// newTestClient points a client at srv with the rate limiter and backoff wound
// right up, so tests never spend real time waiting.
func newTestClient(t *testing.T, srv *httptest.Server, opts ...Option) *Client {
	t.Helper()

	base := []Option{
		WithBaseURL(srv.URL),
		WithRateLimit(1000, 1000),
		WithSleep(func(context.Context, time.Duration) error { return nil }),
		WithLocation(yerevan(t)),
		WithCurrency("AMD"),
	}

	client, err := NewClient(
		testPartnerToken, testUserToken, testCompanyID,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		append(base, opts...)...,
	)
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}
	return client
}

// serveFixture answers every request with one fixture and records the request.
func serveFixture(t *testing.T, name string, status int) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()

	var (
		gotReq  http.Request
		gotBody []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r.Clone(context.Background())
		gotBody, _ = io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)

	return srv, &gotReq, &gotBody
}

func TestNewClientRequiresCredentials(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := NewClient("", testUserToken, testCompanyID, logger); err == nil {
		t.Error("NewClient() accepted an empty partner token")
	}
	if _, err := NewClient(testPartnerToken, testUserToken, "", logger); err == nil {
		t.Error("NewClient() accepted an empty company id")
	}
}

// TestAuthorizationHeader covers Altegio's two-token scheme: the partner token
// alone reaches the public booking endpoints, and business data needs the user
// token appended to the same header.
func TestAuthorizationHeader(t *testing.T) {
	tests := map[string]struct {
		userToken string
		want      string
	}{
		"partner and user": {
			userToken: testUserToken,
			want:      "Bearer " + testPartnerToken + ", User " + testUserToken,
		},
		"partner only": {
			userToken: "",
			want:      "Bearer " + testPartnerToken,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv, gotReq, _ := serveFixture(t, "book_staff.json", http.StatusOK)

			client, err := NewClient(testPartnerToken, tt.userToken, testCompanyID,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				WithBaseURL(srv.URL), WithRateLimit(1000, 1000))
			if err != nil {
				t.Fatalf("NewClient() returned error: %v", err)
			}

			if _, err := client.ListStaff(t.Context()); err != nil {
				t.Fatalf("ListStaff() returned error: %v", err)
			}

			if got := gotReq.Header.Get("Authorization"); got != tt.want {
				t.Errorf("Authorization = %q, want %q", got, tt.want)
			}
			if got := gotReq.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept = %q, want application/json", got)
			}
		})
	}
}

func TestListServices(t *testing.T) {
	srv, gotReq, _ := serveFixture(t, "book_services.json", http.StatusOK)
	client := newTestClient(t, srv)

	services, err := client.ListServices(t.Context())
	if err != nil {
		t.Fatalf("ListServices() returned error: %v", err)
	}

	if want := "/book_services/" + testCompanyID; gotReq.URL.Path != want {
		t.Errorf("path = %q, want %q", gotReq.URL.Path, want)
	}

	// The withdrawn service must not reach the assistant: it cannot be able to
	// offer something the business has taken off the menu.
	if len(services) != 2 {
		t.Fatalf("returned %d services, want 2 with the withdrawn one dropped", len(services))
	}

	first := services[0]
	if first.ID != "1001" {
		t.Errorf("id = %q, want 1001", first.ID)
	}
	if first.Name != "Women's haircut" {
		t.Errorf("name = %q", first.Name)
	}
	if first.Category != "Hair" {
		t.Errorf("category = %q, want Hair", first.Category)
	}
	if first.Duration != time.Hour {
		t.Errorf("duration = %s, want 1h", first.Duration)
	}
	if first.Currency != "AMD" {
		t.Errorf("currency = %q, want AMD", first.Currency)
	}
	if got, want := first.PriceLabel(), "8000-12000 AMD"; got != want {
		t.Errorf("PriceLabel() = %q, want %q", got, want)
	}
}

func TestListStaff(t *testing.T) {
	srv, _, _ := serveFixture(t, "book_staff.json", http.StatusOK)
	client := newTestClient(t, srv)

	staff, err := client.ListStaff(t.Context())
	if err != nil {
		t.Fatalf("ListStaff() returned error: %v", err)
	}

	// Someone who has left the business is dropped entirely.
	if len(staff) != 3 {
		t.Fatalf("returned %d staff, want 3 with the departed one dropped", len(staff))
	}

	byName := make(map[string]booking.Staff, len(staff))
	for _, member := range staff {
		byName[member.Name] = member
	}

	if !byName["Mariam"].Bookable {
		t.Error("Mariam is marked unbookable despite bookable:true")
	}
	// A missing flag means bookable; availability is read separately.
	if !byName["Gor"].Bookable {
		t.Error("a missing bookable flag was treated as unbookable")
	}
	// Someone unavailable is returned marked so, not hidden, because the
	// assistant may need to say a named person cannot be booked.
	if byName["Nare"].Bookable {
		t.Error("Nare is marked bookable despite bookable:false")
	}
	if _, present := byName["Former colleague"]; present {
		t.Error("someone who has left the business was returned")
	}
}

func TestAvailableDates(t *testing.T) {
	srv, gotReq, _ := serveFixture(t, "book_dates.json", http.StatusOK)
	client := newTestClient(t, srv)

	dates, err := client.AvailableDates(t.Context(), "501")
	if err != nil {
		t.Fatalf("AvailableDates() returned error: %v", err)
	}

	if got := gotReq.URL.Query().Get("staff_id"); got != "501" {
		t.Errorf("staff_id = %q, want 501", got)
	}

	// The unreadable entry is skipped rather than losing the whole calendar.
	if len(dates) != 2 {
		t.Fatalf("returned %d dates, want 2", len(dates))
	}

	// Dates are calendar days in the business's timezone, not instants.
	if zone, _ := dates[0].Zone(); zone == "UTC" {
		t.Error("dates were parsed in UTC rather than the business timezone")
	}
	if got := dates[0].Format("2006-01-02 15:04"); got != "2026-09-04 00:00" {
		t.Errorf("first date = %q, want 2026-09-04 00:00", got)
	}
}

func TestAvailableSlots(t *testing.T) {
	srv, gotReq, _ := serveFixture(t, "book_times.json", http.StatusOK)
	client := newTestClient(t, srv)

	day := time.Date(2026, 9, 4, 0, 0, 0, 0, yerevan(t))
	slots, err := client.AvailableSlots(t.Context(), "501", day)
	if err != nil {
		t.Fatalf("AvailableSlots() returned error: %v", err)
	}

	if want := "/book_times/" + testCompanyID + "/501/2026-09-04"; gotReq.URL.Path != want {
		t.Errorf("path = %q, want %q", gotReq.URL.Path, want)
	}

	// Three readable slots; the entry carrying neither a time nor a datetime
	// is skipped rather than becoming a booking at the zero time.
	if len(slots) != 3 {
		t.Fatalf("returned %d slots, want 3", len(slots))
	}

	if got := slots[0].Start.Format(time.RFC3339); got != "2026-09-04T10:00:00+04:00" {
		t.Errorf("first slot = %q", got)
	}
	if slots[0].Duration != time.Hour {
		t.Errorf("duration = %s, want 1h", slots[0].Duration)
	}
	if slots[0].StaffID != "501" {
		t.Errorf("staff = %q, want 501", slots[0].StaffID)
	}
	if got := slots[0].End().Format(time.RFC3339); got != "2026-09-04T11:00:00+04:00" {
		t.Errorf("End() = %q", got)
	}

	// The slot without a datetime falls back to the bare time, which is only
	// correct when read in the business timezone.
	if got := slots[2].Start.Format(time.RFC3339); got != "2026-09-04T15:30:00+04:00" {
		t.Errorf("slot without a datetime = %q, want it read in the business timezone", got)
	}
}

func TestAvailableSlotsRequiresAStaffMember(t *testing.T) {
	srv, _, _ := serveFixture(t, "book_times.json", http.StatusOK)
	client := newTestClient(t, srv)

	_, err := client.AvailableSlots(t.Context(), "", time.Now())
	if !errors.Is(err, booking.ErrRejected) {
		t.Errorf("error = %v, want ErrRejected", err)
	}
}

func validRequest() booking.Request {
	return booking.Request{
		IdempotencyKey: "01a05d0c-df89-786b-a3cb-0d30ff15bf28",
		CustomerID:     "cust-1",
		CustomerName:   "Anna Petrosyan",
		Phone:          "+37411223344",
		ServiceIDs:     []string{"1001"},
		StaffID:        "501",
		StartsAt:       time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
		Duration:       time.Hour,
	}
}

func TestCreateSendsTheIdempotencyKey(t *testing.T) {
	srv, gotReq, gotBody := serveFixture(t, "book_record.json", http.StatusOK)
	client := newTestClient(t, srv)

	result, err := client.Create(t.Context(), validRequest())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if want := "/book_record/" + testCompanyID; gotReq.URL.Path != want {
		t.Errorf("path = %q, want %q", gotReq.URL.Path, want)
	}

	var sent bookRecordRequest
	if err := json.Unmarshal(*gotBody, &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}

	// api_id is what makes a repeat submission recognisable to Altegio rather
	// than a second appointment.
	if sent.APIID != validRequest().IdempotencyKey {
		t.Errorf("api_id = %q, want the idempotency key", sent.APIID)
	}
	if sent.Phone != "+37411223344" {
		t.Errorf("phone = %q", sent.Phone)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(*gotBody, &rawFields); err != nil {
		t.Fatalf("decode raw request fields: %v", err)
	}
	if _, ok := rawFields["fullname"]; !ok {
		t.Error("request has no fullname field required by Altegio")
	}
	if _, ok := rawFields["full_name"]; ok {
		t.Error("request used undocumented full_name instead of fullname")
	}
	if len(sent.Appointments) != 1 {
		t.Fatalf("sent %d appointments, want 1", len(sent.Appointments))
	}
	if sent.Appointments[0].StaffID != 501 {
		t.Errorf("staff_id = %d, want 501", sent.Appointments[0].StaffID)
	}

	// The appointment must be sent in the business's timezone, or it lands at
	// the wrong hour.
	if got := sent.Appointments[0].Datetime; got != "2026-09-04T14:00:00+04:00" {
		t.Errorf("datetime = %q, want the start expressed in the business timezone", got)
	}

	if result.ExternalID != "998877" {
		t.Errorf("external id = %q, want 998877", result.ExternalID)
	}
	if result.Status != booking.StatusConfirmed {
		t.Errorf("status = %q, want confirmed", result.Status)
	}
	if result.ManagementToken != "ab12cd34ef56" {
		t.Errorf("management token = %q, want the returned record_hash", result.ManagementToken)
	}
	if result.Duration != time.Hour {
		t.Errorf("duration = %s, want 1h", result.Duration)
	}
}

func TestCancelUsesThePrivateRecordHashAndAcceptsNoContent(t *testing.T) {
	var gotReq http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r.Clone(context.Background())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.Cancel(t.Context(), booking.Booking{
		ExternalID:      "998877",
		ManagementToken: "ab12cd34ef56",
	})
	if err != nil {
		t.Fatalf("Cancel() returned error: %v", err)
	}
	if gotReq.Method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotReq.Method)
	}
	if want := "/user/records/998877/ab12cd34ef56"; gotReq.URL.Path != want {
		t.Errorf("path = %q, want %q", gotReq.URL.Path, want)
	}
}

func TestCancelRequiresItsPrivateManagementToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Cancel() called Altegio without a management token")
	}))
	defer srv.Close()

	err := newTestClient(t, srv).Cancel(t.Context(), booking.Booking{ExternalID: "998877"})
	if !errors.Is(err, booking.ErrRejected) {
		t.Errorf("error = %v, want ErrRejected", err)
	}
}

func TestCancelTreatsAnAlreadyMissingAppointmentAsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"not found"}}`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv).Cancel(t.Context(), booking.Booking{
		ExternalID: "998877", ManagementToken: "ab12cd34ef56",
	})
	if err != nil {
		t.Errorf("Cancel() = %v, want an already absent appointment accepted", err)
	}
}

func TestRescheduleSetsOneExactTime(t *testing.T) {
	var (
		gotReq  http.Request
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r.Clone(context.Background())
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":998877},"meta":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	original := booking.Booking{ExternalID: "998877", StartsAt: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)}
	newStart := time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC)
	moved, err := client.Reschedule(t.Context(), original, newStart)
	if err != nil {
		t.Fatalf("Reschedule() returned error: %v", err)
	}
	if gotReq.Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotReq.Method)
	}
	if want := "/book_record/" + testCompanyID + "/998877"; gotReq.URL.Path != want {
		t.Errorf("path = %q, want %q", gotReq.URL.Path, want)
	}
	var sent rescheduleRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent.Datetime != "2026-09-04T14:30:00+04:00" {
		t.Errorf("datetime = %q, want it expressed in the business timezone", sent.Datetime)
	}
	if !moved.StartsAt.Equal(newStart) {
		t.Errorf("moved start = %s, want %s", moved.StartsAt, newStart)
	}
	if !original.StartsAt.Equal(time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)) {
		t.Error("Reschedule() mutated its input value")
	}
}

func TestRescheduleRejectionMeansTheSlotWasTaken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(fixture(t, "error_slot_taken.json"))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Reschedule(t.Context(),
		booking.Booking{ExternalID: "998877"}, time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC))
	if !errors.Is(err, booking.ErrSlotUnavailable) {
		t.Errorf("error = %v, want ErrSlotUnavailable", err)
	}
}

func TestRescheduleDoesNotHideRejectedCredentialsAsATakenSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"Wrong token"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Reschedule(t.Context(),
		booking.Booking{ExternalID: "998877"}, time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC))
	if !errors.Is(err, booking.ErrRejected) {
		t.Errorf("error = %v, want ErrRejected", err)
	}
	if errors.Is(err, booking.ErrSlotUnavailable) {
		t.Errorf("credential error = %v, must not look like a customer took the slot", err)
	}
}

func TestUnresolvedManagementRequestReportsUnknownOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"internal error"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv, WithMaxAttempts(1))
	err := client.Cancel(t.Context(), booking.Booking{
		ExternalID: "998877", ManagementToken: "ab12cd34ef56",
	})
	if !errors.Is(err, booking.ErrOutcomeUnknown) {
		t.Errorf("error = %v, want ErrOutcomeUnknown", err)
	}
}

// TestCreateIsNeverRetried is the most important behaviour in this package: a
// repeated booking request risks a customer with two appointments, so a failure
// is reported rather than retried.
func TestCreateIsNeverRetried(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"internal error"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	if _, err := client.Create(t.Context(), validRequest()); err == nil {
		t.Fatal("Create() succeeded against a failing server")
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("the booking request was sent %d times, want exactly 1", got)
	}
}

// TestCreateReportsAnUnknownOutcome covers the case that must never be guessed:
// the request left, the answer never arrived, and the appointment may exist.
func TestCreateReportsAnUnknownOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close() // nothing is listening, so the request fails at the transport

	client, err := NewClient(testPartnerToken, testUserToken, testCompanyID,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithBaseURL(baseURL), WithRateLimit(1000, 1000))
	if err != nil {
		t.Fatalf("NewClient() returned error: %v", err)
	}

	_, createErr := client.Create(t.Context(), validRequest())
	if !errors.Is(createErr, booking.ErrOutcomeUnknown) {
		t.Errorf("error = %v, want ErrOutcomeUnknown", createErr)
	}
}

// TestCreateWithNoAppointmentReturned covers Altegio accepting the call but
// naming no appointment. Whether one exists cannot be told from here.
func TestCreateWithNoAppointmentReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":[],"meta":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	if _, err := client.Create(t.Context(), validRequest()); !errors.Is(err, booking.ErrOutcomeUnknown) {
		t.Errorf("error = %v, want ErrOutcomeUnknown", err)
	}
}

func TestCreateRejectsNonNumericIdentifiers(t *testing.T) {
	srv, _, _ := serveFixture(t, "book_record.json", http.StatusOK)
	client := newTestClient(t, srv)

	req := validRequest()
	req.StaffID = "not-a-number"

	if _, err := client.Create(t.Context(), req); !errors.Is(err, booking.ErrRejected) {
		t.Errorf("error = %v, want ErrRejected", err)
	}
}

func TestCheckReportsATakenSlot(t *testing.T) {
	srv, _, _ := serveFixture(t, "error_slot_taken.json", http.StatusUnprocessableEntity)
	client := newTestClient(t, srv)

	err := client.Check(t.Context(), validRequest().Selection())
	if !errors.Is(err, booking.ErrSlotUnavailable) {
		t.Errorf("error = %v, want ErrSlotUnavailable", err)
	}
	if !strings.Contains(err.Error(), "already taken") {
		t.Errorf("error %v does not carry Altegio's explanation", err)
	}
}

func TestCheckPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":null,"meta":[]}`))
	}))
	defer srv.Close()

	if err := newTestClient(t, srv).Check(t.Context(), validRequest().Selection()); err != nil {
		t.Errorf("Check() returned error: %v", err)
	}
}

func TestReadsAreRetriedThenGiveUp(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"bad gateway"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv, WithMaxAttempts(3))
	_, err := client.ListServices(t.Context())

	if !errors.Is(err, booking.ErrUnavailable) {
		t.Errorf("error = %v, want ErrUnavailable", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

func TestReadsRecoverFromATransientFailure(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"try again"}}`))
			return
		}
		_, _ = w.Write(fixture(t, "book_staff.json"))
	}))
	defer srv.Close()

	staff, err := newTestClient(t, srv).ListStaff(t.Context())
	if err != nil {
		t.Fatalf("ListStaff() returned error: %v", err)
	}
	if len(staff) == 0 {
		t.Error("the retry returned nothing")
	}
}

// TestCredentialFailuresAreNotRetried: wrong credentials fail identically every
// time, and retrying only burns the rate limit.
func TestCredentialFailuresAreNotRetried(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"Wrong partner token"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ListServices(t.Context())
	if !errors.Is(err, booking.ErrRejected) {
		t.Errorf("error = %v, want ErrRejected", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d attempts on rejected credentials, want 1", got)
	}
}

func TestMissingResourcesAreReportedAsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"Location not found"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ListServices(t.Context())
	if !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// TestTokensNeverAppearInErrors: Altegio credentials travel in a header rather
// than the URL, and nothing should put them back into a message that is logged.
func TestTokensNeverAppearInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"meta":{"message":"Wrong token"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ListServices(t.Context())
	if err == nil {
		t.Fatal("ListServices() succeeded")
	}
	for _, secret := range []string{testPartnerToken, testUserToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("a credential leaked into an error: %v", err)
		}
	}
}

func TestCancellationStopsWaiting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		_, _ = w.Write(fixture(t, "book_staff.json"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if _, err := newTestClient(t, srv).ListStaff(ctx); err == nil {
		t.Error("ListStaff() succeeded despite the deadline")
	}
}
