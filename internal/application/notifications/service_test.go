package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
)

type testRepository struct {
	mu          sync.Mutex
	rows        map[string]Entry
	failReceipt bool
}

func (r *testRepository) TransactNotifications(_ context.Context, keys []string, change func(map[string]*Entry) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := map[string]*Entry{}
	for _, key := range keys {
		raw, _ := json.Marshal(r.rows[key])
		entry := new(Entry)
		_ = json.Unmarshal(raw, entry)
		rows[key] = entry
	}
	if err := change(rows); err != nil {
		return err
	}
	for _, key := range keys {
		if r.failReceipt && rows[key].Kind == "delivery" && rows[key].State == "done" {
			return errors.New("receipt write failed")
		}
	}
	for _, key := range keys {
		r.rows[key] = *rows[key]
	}
	return nil
}
func (r *testRepository) GetNotification(_ context.Context, key string) (Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows[key], nil
}
func (r *testRepository) PendingNotifications(_ context.Context) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []Entry
	for _, row := range r.rows {
		if row.Kind == "event" && (row.State == "pending" || row.State == "prepared") {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

type testScheduler struct{}

func (testScheduler) ScheduleNotification(context.Context, string, string, time.Time) error {
	return nil
}

type testReader struct{ snapshot Snapshot }

func (r *testReader) ReadNativeBooking(context.Context, string) (Snapshot, error) {
	return r.snapshot, nil
}

type testOwned struct{ found bool }

func (o testOwned) FindBooking(context.Context, string) (booking.Booking, error) {
	if o.found {
		return booking.Booking{}, nil
	}
	return booking.Booking{}, booking.ErrNotFound
}

type testSender struct {
	mu        sync.Mutex
	channels  []Channel
	reject    bool
	uncertain bool
}

func (s *testSender) SendNotification(_ context.Context, t Target, _ Notice, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels = append(s.channels, t.Channel)
	if s.uncertain {
		return false, errors.New("timeout after send")
	}
	if s.reject && t.Channel == Telegram {
		return true, errors.New("blocked")
	}
	return false, nil
}
func notificationFixture(t *testing.T) (*Service, *testRepository, *testReader, *testSender) {
	t.Helper()
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	repo := &testRepository{rows: map[string]Entry{}}
	reader := &testReader{snapshot: Snapshot{Phone: "+37491123456", ChangedAt: now, Booking: booking.Booking{ExternalID: "42", CustomerName: "Test client", ServiceIDs: []string{"1"}, ServiceNames: []string{"Test treatment"}, StaffID: "2", StaffName: "Test specialist", StartsAt: now.Add(time.Hour), Duration: time.Hour, Status: booking.StatusConfirmed, CreatedAt: now}}}
	sender := new(testSender)
	service := &Service{Repo: repo, Reader: reader, Scheduler: testScheduler{}, Owned: testOwned{}, Sender: sender, ActivatedAt: now.Add(-time.Minute), Now: func() time.Time { return now }, WhatsAppTemplates: map[string]string{"booking_created:en": "approved_fixture", "booking_changed:en": "approved_fixture", "booking_cancelled:en": "approved_fixture"}}
	if err := service.RequestContact(context.Background(), "123", "en"); err != nil {
		t.Fatal(err)
	}
	if err := service.LinkTelegram(context.Background(), "123", reader.snapshot.Phone); err != nil {
		t.Fatal(err)
	}
	if err := service.LinkWhatsApp(context.Background(), reader.snapshot.Phone, "en", true); err != nil {
		t.Fatal(err)
	}
	return service, repo, reader, sender
}
func ingestAndDeliver(t *testing.T, s *Service, status string) string {
	t.Helper()
	e := Event{ID: uuid.NewString(), RecordID: "42", Status: status}
	if err := s.Ingest(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	return e.ID
}
func TestNativeBookingDeduplicatesTransportAndSemanticEvents(t *testing.T) {
	s, _, _, sender := notificationFixture(t)
	id := ingestAndDeliver(t, s, "create")
	if err := s.Ingest(context.Background(), Event{ID: id, RecordID: "42", Status: "create"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	ingestAndDeliver(t, s, "update")
	if len(sender.channels) != 1 || sender.channels[0] != Telegram {
		t.Fatalf("channels: %v", sender.channels)
	}
}
func TestNativeBookingFallbackOnlyOnCertainRejection(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "uncertain"}[uncertain], func(t *testing.T) {
			s, _, _, sender := notificationFixture(t)
			sender.reject = true
			sender.uncertain = uncertain
			ingestAndDeliver(t, s, "create")
			expected := 2
			if uncertain {
				expected = 1
			}
			if len(sender.channels) != expected {
				t.Fatalf("channels: %v", sender.channels)
			}
			for _, c := range sender.channels {
				if c == SMS {
					t.Fatal("SMS must never be sent")
				}
			}
		})
	}
}
func TestNativeBookingReceiptFailureDoesNotRetryOrFallback(t *testing.T) {
	s, repo, _, sender := notificationFixture(t)
	repo.failReceipt = true
	e := Event{ID: uuid.NewString(), RecordID: "42", Status: "create"}
	if err := s.Ingest(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), e.ID); err == nil {
		t.Fatal("expected receipt failure")
	}
	repo.failReceipt = false
	// Consent or channel priority may change between attempts. A persisted send
	// intent must still prevent fallback after an ambiguous acceptance.
	if err := s.UnlinkTelegram(context.Background(), "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	if len(sender.channels) != 1 {
		t.Fatalf("duplicate/fallback after unknown acceptance: %v", sender.channels)
	}
}
func TestNativeBookingChangesCancellationAndOlderSnapshots(t *testing.T) {
	s, _, reader, sender := notificationFixture(t)
	ingestAndDeliver(t, s, "create")
	original := reader.snapshot
	reader.snapshot.Booking.StartsAt = reader.snapshot.Booking.StartsAt.Add(time.Hour)
	reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(time.Minute)
	ingestAndDeliver(t, s, "update")
	latest := reader.snapshot
	reader.snapshot = original
	ingestAndDeliver(t, s, "update")
	if len(sender.channels) != 2 {
		t.Fatal("stale snapshot generated a notification")
	}
	reader.snapshot = original
	reader.snapshot.ChangedAt = latest.ChangedAt.Add(time.Minute)
	ingestAndDeliver(t, s, "update")
	if len(sender.channels) != 3 {
		t.Fatal("returning to the original time must be announced")
	}
	reader.snapshot = latest
	reader.snapshot.Booking.Status = booking.StatusCancelled
	reader.snapshot.ChangedAt = reader.snapshot.ChangedAt.Add(2 * time.Minute)
	ingestAndDeliver(t, s, "delete")
	if len(sender.channels) != 4 {
		t.Fatalf("channels %v", sender.channels)
	}
}
func TestNativeBookingDoesNotSendHistoricalOrAssistantBookings(t *testing.T) {
	for _, kind := range []string{"historical", "owned", "api"} {
		t.Run(kind, func(t *testing.T) {
			s, _, reader, sender := notificationFixture(t)
			switch kind {
			case "historical":
				reader.snapshot.Booking.CreatedAt = s.ActivatedAt.Add(-time.Second)
			case "owned":
				s.Owned = testOwned{found: true}
			case "api":
				reader.snapshot.APIID = "omnichannel-test"
			}
			ingestAndDeliver(t, s, "create")
			if len(sender.channels) > 0 {
				t.Fatal("sent unwanted confirmation")
			}
		})
	}
}
func TestNativeBookingConcurrentEventsSendOnce(t *testing.T) {
	s, _, _, sender := notificationFixture(t)
	var wg sync.WaitGroup
	for range 20 {
		e := Event{ID: uuid.NewString(), RecordID: "42", Status: "create"}
		if err := s.Ingest(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			if err := s.Deliver(context.Background(), e.ID); err != nil && !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(sender.channels) != 1 {
		t.Fatalf("concurrent sends %v", sender.channels)
	}
}
func TestPhoneLinkRequiresRecentOptInAndRefusesOtherChat(t *testing.T) {
	s, _, reader, _ := notificationFixture(t)
	if err := s.LinkTelegram(context.Background(), "other", reader.snapshot.Phone); err == nil {
		t.Fatal("unrequested phone accepted")
	}
	if err := s.RequestContact(context.Background(), "other", "hy"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkTelegram(context.Background(), "other", reader.snapshot.Phone); err == nil {
		t.Fatal("another chat stole a phone link")
	}
	if err := s.UnlinkTelegram(context.Background(), "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkTelegram(context.Background(), "other", reader.snapshot.Phone); err != nil {
		t.Fatal(err)
	}
	now := s.now()
	s.Now = func() time.Time { return now.Add(11 * time.Minute) }
	if err := s.LinkTelegram(context.Background(), "123", "+37499123456"); err == nil {
		t.Fatal("expired contact request accepted")
	}
}
func TestNativeBookingWhatsAppFirstUsesApprovedOptIn(t *testing.T) {
	s, repo, _, sender := notificationFixture(t)
	if err := repo.TransactNotifications(context.Background(), []string{"policy"}, func(rows map[string]*Entry) error { rows["policy"].Order = "whatsapp"; return nil }); err != nil {
		t.Fatal(err)
	}
	ingestAndDeliver(t, s, "create")
	if len(sender.channels) != 1 || sender.channels[0] != WhatsApp {
		t.Fatalf("channels %v", sender.channels)
	}
	s, _, _, sender = notificationFixture(t)
	s.WhatsAppTemplates = nil
	if err := s.UnlinkTelegram(context.Background(), "123"); err != nil {
		t.Fatal(err)
	}

	ingestAndDeliver(t, s, "create")
	if len(sender.channels) != 0 {
		t.Fatal("unapproved WhatsApp template sent")
	}
}
