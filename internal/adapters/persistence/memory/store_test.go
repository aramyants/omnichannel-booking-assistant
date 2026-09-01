package memory

import (
	"errors"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/customer"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func identity(customerID, externalUserID string) customer.ChannelIdentity {
	return customer.ChannelIdentity{
		ID:             "identity-" + externalUserID,
		CustomerID:     customerID,
		Provider:       messaging.ProviderTelegram,
		ExternalUserID: externalUserID,
		DisplayName:    "Anna",
	}
}

func TestFindOrCreateReturnsTheSameCustomerForAKnownIdentity(t *testing.T) {
	store := New()

	first, err := store.FindOrCreateByChannelIdentity(t.Context(),
		identity("cust-1", "219847362"), customer.Customer{ID: "cust-1", Name: "Anna"})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}

	// A second message from the same account must not create a second customer,
	// which would split one person's history across two records.
	second, err := store.FindOrCreateByChannelIdentity(t.Context(),
		identity("cust-2", "219847362"), customer.Customer{ID: "cust-2", Name: "Anna"})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("the same identity produced customers %q and %q", first.ID, second.ID)
	}
	if second.ID != "cust-1" {
		t.Errorf("customer = %q, want the one created first", second.ID)
	}
}

func TestDifferentIdentitiesAreDifferentCustomers(t *testing.T) {
	store := New()

	first, _ := store.FindOrCreateByChannelIdentity(t.Context(),
		identity("cust-1", "111"), customer.Customer{ID: "cust-1"})
	second, _ := store.FindOrCreateByChannelIdentity(t.Context(),
		identity("cust-2", "222"), customer.Customer{ID: "cust-2"})

	if first.ID == second.ID {
		t.Error("two different accounts were resolved to one customer")
	}
}

// TestSameExternalIDOnDifferentChannelsStaysSeparate guards the rule that
// identities are never merged on a coincidence. Two providers can hand out the
// same numeric user id to different people.
func TestSameExternalIDOnDifferentChannelsStaysSeparate(t *testing.T) {
	store := New()

	telegram := identity("cust-1", "12345")
	whatsapp := identity("cust-2", "12345")
	whatsapp.Provider = messaging.ProviderWhatsApp

	first, _ := store.FindOrCreateByChannelIdentity(t.Context(), telegram, customer.Customer{ID: "cust-1"})
	second, _ := store.FindOrCreateByChannelIdentity(t.Context(), whatsapp, customer.Customer{ID: "cust-2"})

	if first.ID == second.ID {
		t.Error("a matching user id on two channels was treated as one person")
	}
}

func TestFindOrCreateRejectsIncompleteIdentities(t *testing.T) {
	store := New()

	broken := identity("cust-1", "219847362")
	broken.ExternalUserID = ""

	if _, err := store.FindOrCreateByChannelIdentity(t.Context(), broken, customer.Customer{ID: "cust-1"}); err == nil {
		t.Error("an incomplete identity was accepted")
	}
}

func conv(id, threadID string) conversation.Conversation {
	return conversation.Conversation{
		ID:               id,
		CustomerID:       "cust-1",
		Provider:         messaging.ProviderTelegram,
		ExternalThreadID: threadID,
		State:            conversation.StateAssistantActive,
	}
}

func TestFindOrOpenReusesAThread(t *testing.T) {
	store := New()

	first, err := store.FindOrOpen(t.Context(), conv("conv-1", "219847362"))
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	second, err := store.FindOrOpen(t.Context(), conv("conv-2", "219847362"))
	if err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("one thread produced conversations %q and %q", first.ID, second.ID)
	}
}

func TestSaveThenFindByID(t *testing.T) {
	store := New()

	original, _ := store.FindOrOpen(t.Context(), conv("conv-1", "219847362"))
	original.State = conversation.StateHumanActive
	if err := store.Save(t.Context(), original); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	loaded, err := store.FindByID(t.Context(), "conv-1")
	if err != nil {
		t.Fatalf("FindByID() returned error: %v", err)
	}
	if loaded.State != conversation.StateHumanActive {
		t.Errorf("state = %q, want %q", loaded.State, conversation.StateHumanActive)
	}

	// The saved state must also be what a later lookup by thread returns.
	reopened, _ := store.FindOrOpen(t.Context(), conv("conv-2", "219847362"))
	if reopened.State != conversation.StateHumanActive {
		t.Errorf("lookup by thread returned state %q, want %q", reopened.State, conversation.StateHumanActive)
	}
}

func TestFindByIDReportsMissingConversations(t *testing.T) {
	if _, err := New().FindByID(t.Context(), "nope"); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestRecentReturnsTheNewestMessagesOldestFirst(t *testing.T) {
	store := New()

	for i := range 5 {
		if err := store.Append(t.Context(), conversation.Message{
			ID:             string(rune('a' + i)),
			ConversationID: "conv-1",
			Text:           string(rune('a' + i)),
		}); err != nil {
			t.Fatalf("Append() returned error: %v", err)
		}
	}

	got, err := store.Recent(t.Context(), "conv-1", 3)
	if err != nil {
		t.Fatalf("Recent() returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("returned %d messages, want 3", len(got))
	}
	for i, want := range []string{"c", "d", "e"} {
		if got[i].Text != want {
			t.Errorf("message %d = %q, want %q", i, got[i].Text, want)
		}
	}
}

// TestRecentDoesNotShareStorage guards against a caller mutating stored state
// through the returned slice, which no durable store would permit either.
func TestRecentDoesNotShareStorage(t *testing.T) {
	store := New()
	_ = store.Append(t.Context(), conversation.Message{ID: "m1", ConversationID: "conv-1", Text: "original"})

	got, _ := store.Recent(t.Context(), "conv-1", 10)
	got[0].Text = "tampered"

	again, _ := store.Recent(t.Context(), "conv-1", 10)
	if again[0].Text != "original" {
		t.Errorf("stored message was mutated through the returned slice: %q", again[0].Text)
	}
}

func TestRecentOnAnUnknownConversation(t *testing.T) {
	got, err := New().Recent(t.Context(), "nope", 10)
	if err != nil {
		t.Fatalf("Recent() returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("returned %d messages, want none", len(got))
	}
}

func TestProcessedEvents(t *testing.T) {
	now := time.Unix(1756728000, 0).UTC()
	store := New(WithClock(func() time.Time { return now }))

	seen, err := store.Seen(t.Context(), "telegram:4127")
	if err != nil {
		t.Fatalf("Seen() returned error: %v", err)
	}
	if seen {
		t.Error("an unhandled delivery reported as seen")
	}

	if err := store.MarkProcessed(t.Context(), "telegram:4127", now); err != nil {
		t.Fatalf("MarkProcessed() returned error: %v", err)
	}

	seen, _ = store.Seen(t.Context(), "telegram:4127")
	if !seen {
		t.Error("a handled delivery reported as unseen, so a retry would be answered twice")
	}

	if seen, _ = store.Seen(t.Context(), "telegram:4128"); seen {
		t.Error("a different message reported as seen")
	}
}

func TestProcessedEventsExpire(t *testing.T) {
	now := time.Unix(1756728000, 0).UTC()
	clock := func() time.Time { return now }
	store := New(WithClock(func() time.Time { return clock() }), WithProcessedTTL(time.Hour))

	_ = store.MarkProcessed(t.Context(), "telegram:4127", now)

	now = now.Add(30 * time.Minute)
	if seen, _ := store.Seen(t.Context(), "telegram:4127"); !seen {
		t.Error("a delivery was forgotten while a retry could still arrive")
	}

	now = now.Add(time.Hour)
	if seen, _ := store.Seen(t.Context(), "telegram:4127"); seen {
		t.Error("an expired delivery is still remembered, so the store grows without bound")
	}
}

// TestConcurrentIdentityResolution is the reason one mutex guards the whole
// store: a customer sending two messages at once must not be created twice.
func TestConcurrentIdentityResolution(t *testing.T) {
	store := New()

	const attempts = 50
	results := make(chan string, attempts)

	for i := range attempts {
		go func() {
			customerID := "cust-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			cust, err := store.FindOrCreateByChannelIdentity(t.Context(),
				identity(customerID, "219847362"),
				customer.Customer{ID: customerID, Name: "Anna"},
			)
			if err != nil {
				results <- ""
				return
			}
			results <- cust.ID
		}()
	}

	first := <-results
	if first == "" {
		t.Fatal("a concurrent call returned an error")
	}
	for range attempts - 1 {
		if got := <-results; got != first {
			t.Fatalf("concurrent calls resolved to customers %q and %q", first, got)
		}
	}
}
