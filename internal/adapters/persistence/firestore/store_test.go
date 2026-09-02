package firestore

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/booking"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

var testNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// newStore connects to the Firestore emulator.
//
// These tests are skipped unless the emulator is running, so an ordinary
// `go test ./...` stays fast and needs nothing installed. Run `make emulator`
// and then `make test-firestore` to exercise them.
func newStore(t *testing.T) *Store {
	t.Helper()

	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run these; see make emulator")
	}

	store, err := New(t.Context(), "test-project")
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store
}

// unique keeps tests independent without clearing the emulator between them,
// including when the suite is rerun against the same long-lived container.
func unique(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + id.New()
}

func TestFindOrCreateResolvesOneCustomerPerIdentity(t *testing.T) {
	store := newStore(t)
	externalID := unique(t, "tg")

	identity := customer.ChannelIdentity{
		ID:             "identity-1",
		CustomerID:     "cust-1",
		Provider:       messaging.ProviderTelegram,
		ExternalUserID: externalID,
		DisplayName:    "Anna",
		CreatedAt:      testNow,
	}

	first, err := store.FindOrCreateByChannelIdentity(t.Context(), identity,
		customer.Customer{ID: "cust-1", Name: "Anna", CreatedAt: testNow, UpdatedAt: testNow})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}

	// A second message from the same account must not create a second customer.
	second := identity
	second.CustomerID = "cust-2"
	resolved, err := store.FindOrCreateByChannelIdentity(t.Context(), second,
		customer.Customer{ID: "cust-2", Name: "Anna", CreatedAt: testNow, UpdatedAt: testNow})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}

	if resolved.ID != first.ID {
		t.Errorf("the same identity produced customers %q and %q", first.ID, resolved.ID)
	}
	if resolved.Name != "Anna" {
		t.Errorf("name = %q, want it read back from the stored record", resolved.Name)
	}
}

// TestConcurrentIdentityResolution is why the lookup runs in a transaction:
// without one, both requests read "no such identity" before either writes.
func TestConcurrentIdentityResolution(t *testing.T) {
	store := newStore(t)
	externalID := unique(t, "tg-concurrent")

	const attempts = 8
	results := make(chan string, attempts)

	for i := range attempts {
		go func() {
			customerID := "cust-" + string(rune('a'+i))
			resolved, err := store.FindOrCreateByChannelIdentity(t.Context(),
				customer.ChannelIdentity{
					ID:             "identity-" + customerID,
					CustomerID:     customerID,
					Provider:       messaging.ProviderTelegram,
					ExternalUserID: externalID,
					CreatedAt:      testNow,
				},
				customer.Customer{ID: customerID, CreatedAt: testNow, UpdatedAt: testNow},
			)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- resolved.ID
		}()
	}

	first := <-results
	for range attempts - 1 {
		if got := <-results; got != first {
			t.Fatalf("concurrent calls resolved to %q and %q", first, got)
		}
	}
}

func conv(t *testing.T) conversation.Conversation {
	t.Helper()
	return conversation.Conversation{
		ID:               unique(t, "conv"),
		CustomerID:       "cust-1",
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: unique(t, "thread"),
		State:            conversation.StateAssistantActive,
		CreatedAt:        testNow,
		UpdatedAt:        testNow,
		LastMessageAt:    testNow,
	}
}

func TestConversationRoundTrip(t *testing.T) {
	store := newStore(t)
	candidate := conv(t)

	opened, err := store.FindOrOpen(t.Context(), candidate)
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	if opened.ID != candidate.ID {
		t.Errorf("id = %q, want %q", opened.ID, candidate.ID)
	}

	// Reopening the same thread returns the stored conversation.
	second := candidate
	second.ID = "different-id"
	reopened, err := store.FindOrOpen(t.Context(), second)
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	if reopened.ID != candidate.ID {
		t.Errorf("one thread produced conversations %q and %q", candidate.ID, reopened.ID)
	}
}

// TestDraftSurvivesARoundTrip matters because the draft is what a confirmation
// reads: losing it between messages means losing the booking the customer
// agreed to.
func TestDraftSurvivesARoundTrip(t *testing.T) {
	store := newStore(t)
	candidate := conv(t)

	opened, err := store.FindOrOpen(t.Context(), candidate)
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}

	opened.Draft = &booking.Draft{
		IdempotencyKey: "key-1",
		ServiceIDs:     []string{"1001"},
		ServiceNames:   []string{"Women's haircut"},
		StaffID:        "501",
		StaffName:      "Mariam",
		StartsAt:       testNow.Add(48 * time.Hour),
		Duration:       time.Hour,
		Phone:          "+37411223344",
		CustomerName:   "Anna Petrosyan",
		PreparedAt:     testNow,
	}
	if err := store.Save(t.Context(), opened); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	loaded, err := store.FindOrOpen(t.Context(), candidate)
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	if loaded.Draft == nil {
		t.Fatal("the draft did not survive")
	}
	if loaded.Draft.IdempotencyKey != "key-1" {
		t.Errorf("key = %q", loaded.Draft.IdempotencyKey)
	}
	if loaded.Draft.Duration != time.Hour {
		t.Errorf("duration = %s, want 1h", loaded.Draft.Duration)
	}
	if !loaded.Draft.StartsAt.Equal(testNow.Add(48 * time.Hour)) {
		t.Errorf("starts at = %s", loaded.Draft.StartsAt)
	}

	// Clearing it must also persist, or a spent draft could be confirmed twice.
	loaded.Draft = nil
	if err := store.Save(t.Context(), loaded); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}
	cleared, err := store.FindOrOpen(t.Context(), candidate)
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	if cleared.Draft != nil {
		t.Error("a cleared draft came back")
	}
}

func TestFindByID(t *testing.T) {
	store := newStore(t)
	candidate := conv(t)

	if _, err := store.FindOrOpen(t.Context(), candidate); err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}

	loaded, err := store.FindByID(t.Context(), candidate.ID)
	if err != nil {
		t.Fatalf("FindByID() returned error: %v", err)
	}
	if loaded.ExternalThreadID != candidate.ExternalThreadID {
		t.Errorf("thread = %q", loaded.ExternalThreadID)
	}

	if _, err := store.FindByID(t.Context(), "does-not-exist"); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// TestTranscriptReadsBackInOrder relies on message ids being UUIDv7, which sort
// chronologically, so the order comes from the document key alone.
func TestTranscriptReadsBackInOrder(t *testing.T) {
	store := newStore(t)
	conversationID := unique(t, "conv")

	for _, id := range []string{"01a0-0001", "01a0-0002", "01a0-0003", "01a0-0004"} {
		if err := store.Append(t.Context(), conversation.Message{
			ID:             id,
			ConversationID: conversationID,
			Direction:      conversation.DirectionInbound,
			ContentType:    messaging.ContentTypeText,
			Text:           id,
			CreatedAt:      testNow,
		}); err != nil {
			t.Fatalf("Append() returned error: %v", err)
		}
	}

	got, err := store.Recent(t.Context(), conversationID, 3)
	if err != nil {
		t.Fatalf("Recent() returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("returned %d messages, want 3", len(got))
	}

	// The newest three, oldest first.
	for i, want := range []string{"01a0-0002", "01a0-0003", "01a0-0004"} {
		if got[i].Text != want {
			t.Errorf("message %d = %q, want %q", i, got[i].Text, want)
		}
	}
}

func TestProcessedEvents(t *testing.T) {
	store := newStore(t)
	key := unique(t, "telegram:msg")

	claimed, err := store.Claim(t.Context(), key, "claim-1", testNow)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if !claimed {
		t.Fatal("the first delivery did not acquire its claim")
	}

	claimed, err = store.Claim(t.Context(), key, "claim-2", testNow)
	if err != nil {
		t.Fatalf("second Claim() returned error: %v", err)
	}
	if claimed {
		t.Error("a concurrent delivery stole an active claim")
	}

	if err := store.Complete(t.Context(), key, "claim-1", testNow); err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}
	claimed, err = store.Claim(t.Context(), key, "claim-3", testNow)
	if err != nil {
		t.Fatalf("third Claim() returned error: %v", err)
	}
	if claimed {
		t.Error("a completed delivery became claimable")
	}
}

// TestExpiredProcessedEventsAreIgnored: the TTL policy deletes lazily, so an
// expired document can still be present and must read as unseen either way.
func TestExpiredProcessedEventsAreIgnored(t *testing.T) {
	store := newStore(t)
	key := unique(t, "telegram:expired")

	old := testNow.Add(-processedRetention - time.Hour)
	claimed, err := store.Claim(t.Context(), key, "old-claim", old)
	if err != nil || !claimed {
		t.Fatalf("old Claim() = %t, %v", claimed, err)
	}
	if err := store.Complete(t.Context(), key, "old-claim", old); err != nil {
		t.Fatalf("Complete() returned error: %v", err)
	}

	claimed, err = store.Claim(t.Context(), key, "retry", testNow)
	if err != nil {
		t.Fatalf("retry Claim() returned error: %v", err)
	}
	if !claimed {
		t.Error("an expired delivery is still blocking retries")
	}
}

func TestFailedClaimCanBeRetriedImmediately(t *testing.T) {
	store := newStore(t)
	key := unique(t, "telegram:failed")

	_, _ = store.Claim(t.Context(), key, "failed", testNow)
	if err := store.Release(t.Context(), key, "failed"); err != nil {
		t.Fatalf("Release() returned error: %v", err)
	}
	if claimed, err := store.Claim(t.Context(), key, "retry", testNow); err != nil || !claimed {
		t.Errorf("retry Claim() = %t, %v", claimed, err)
	}
}

func TestAbandonedClaimExpires(t *testing.T) {
	store := newStore(t)
	key := unique(t, "telegram:crashed")

	_, _ = store.Claim(t.Context(), key, "crashed", testNow)
	claimed, err := store.Claim(t.Context(), key, "retry", testNow.Add(claimRetention+time.Second))
	if err != nil || !claimed {
		t.Errorf("retry Claim() = %t, %v", claimed, err)
	}
}

func TestBookingsRoundTrip(t *testing.T) {
	store := newStore(t)
	customerID := unique(t, "cust")

	later := booking.Booking{
		ID: "bk-2", ExternalID: unique(t, "ext-2"), CustomerID: customerID,
		ServiceIDs: []string{"1001"}, StaffID: "501",
		StartsAt: testNow.Add(72 * time.Hour), Duration: time.Hour,
		Status: booking.StatusConfirmed, CreatedAt: testNow,
	}
	sooner := booking.Booking{
		ID: "bk-1", ExternalID: unique(t, "ext-1"), CustomerID: customerID,
		ServiceIDs: []string{"1001"}, StaffID: "501",
		StartsAt: testNow.Add(24 * time.Hour), Duration: time.Hour,
		Status: booking.StatusConfirmed, CreatedAt: testNow,
	}

	for _, b := range []booking.Booking{later, sooner} {
		if err := store.SaveBooking(t.Context(), b); err != nil {
			t.Fatalf("SaveBooking() returned error: %v", err)
		}
	}

	got, err := store.ListBookings(t.Context(), customerID)
	if err != nil {
		t.Fatalf("ListBookings() returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d bookings, want 2", len(got))
	}
	if !got[0].StartsAt.Before(got[1].StartsAt) {
		t.Error("bookings are not soonest first")
	}
	if got[0].Duration != time.Hour {
		t.Errorf("duration = %s, want 1h", got[0].Duration)
	}
}

// TestSavingTheSameBookingTwiceUpdatesIt: documents are keyed by the scheduling
// system's identifier, so a repeated write cannot produce two records.
func TestSavingTheSameBookingTwiceUpdatesIt(t *testing.T) {
	store := newStore(t)
	customerID := unique(t, "cust")
	externalID := unique(t, "ext")

	b := booking.Booking{
		ID: "bk-1", ExternalID: externalID, CustomerID: customerID,
		StartsAt: testNow.Add(24 * time.Hour), Status: booking.StatusConfirmed,
	}
	if err := store.SaveBooking(t.Context(), b); err != nil {
		t.Fatalf("SaveBooking() returned error: %v", err)
	}

	b.Status = booking.StatusCancelled
	if err := store.SaveBooking(t.Context(), b); err != nil {
		t.Fatalf("SaveBooking() returned error: %v", err)
	}

	got, err := store.ListBookings(t.Context(), customerID)
	if err != nil {
		t.Fatalf("ListBookings() returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d bookings, want 1", len(got))
	}
	if got[0].Status != booking.StatusCancelled {
		t.Errorf("status = %q, want the update to have replaced it", got[0].Status)
	}
}

func TestNewRequiresAProject(t *testing.T) {
	if _, err := New(t.Context(), ""); err == nil {
		t.Error("New() accepted an empty project id")
	}
}
