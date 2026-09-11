package firestore

import (
	"errors"
	"testing"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
	"github.com/aramyants/omnichannel-booking-assistant/internal/platform/id"
)

func staffEcho(t *testing.T, externalID string) conversation.Message {
	t.Helper()
	return conversation.Message{
		ID:                id.New(),
		Direction:         conversation.DirectionOutbound,
		ContentType:       messaging.ContentTypeText,
		Text:              "I am checking your appointment personally.",
		ExternalMessageID: externalID,
		CreatedAt:         testNow,
	}
}

// TestExternalReplyTakesOverAndIgnoresReplays covers the whole life of a staff
// message sent from the phone: it opens the conversation if needed, hands it to
// the colleague, is stored once however often Meta redelivers it, and does not
// undo a later resume when it arrives late.
func TestExternalReplyTakesOverAndIgnoresReplays(t *testing.T) {
	store := newStore(t)
	candidate := conv(t)
	candidate.Provider = messaging.ProviderWhatsApp
	echoID := unique(t, "wamid")

	// No conversation exists yet: the echo opens one under the candidate's id.
	if err := store.RecordExternalReply(opCtx(t), candidate, staffEcho(t, echoID), testNow, testNow); err != nil {
		t.Fatalf("RecordExternalReply() returned error: %v", err)
	}
	loaded, err := store.FindByID(opCtx(t), candidate.ID)
	if err != nil {
		t.Fatalf("FindByID() returned error: %v", err)
	}
	if loaded.State != conversation.StateHumanActive || loaded.ExternalReplyRevision != 1 {
		t.Fatalf("state = %q revision = %d, want human_active and 1", loaded.State, loaded.ExternalReplyRevision)
	}

	// A redelivery of the same echo changes nothing.
	if err := store.RecordExternalReply(opCtx(t), candidate, staffEcho(t, echoID), testNow, testNow.Add(time.Second)); err != nil {
		t.Fatalf("RecordExternalReply() replay returned error: %v", err)
	}
	loaded, _ = store.FindByID(opCtx(t), candidate.ID)
	history, err := store.Recent(opCtx(t), candidate.ID, 10)
	if err != nil {
		t.Fatalf("Recent() returned error: %v", err)
	}
	if loaded.ExternalReplyRevision != 1 || len(history) != 1 {
		t.Fatalf("replay moved revision to %d and transcript to %d messages", loaded.ExternalReplyRevision, len(history))
	}
	if history[0].Direction != conversation.DirectionOutbound || history[0].ExternalMessageID != echoID {
		t.Errorf("transcript entry = %+v", history[0])
	}

	// Staff hand the conversation back. An echo sent before that moment is
	// history and must not take it away again; one sent after it does.
	resumedAt := testNow.Add(time.Minute)
	if err := loaded.TransitionTo(conversation.StateAssistantActive, resumedAt); err != nil {
		t.Fatalf("TransitionTo() returned error: %v", err)
	}
	if err := store.Save(opCtx(t), loaded); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}
	if err := store.RecordExternalReply(opCtx(t), candidate, staffEcho(t, unique(t, "late")), testNow, resumedAt.Add(time.Second)); err != nil {
		t.Fatalf("RecordExternalReply() late echo returned error: %v", err)
	}
	loaded, _ = store.FindByID(opCtx(t), candidate.ID)
	history, _ = store.Recent(opCtx(t), candidate.ID, 10)
	if loaded.State != conversation.StateAssistantActive || loaded.ExternalReplyRevision != 1 || len(history) != 2 {
		t.Fatalf("late echo: state = %q revision = %d transcript = %d", loaded.State, loaded.ExternalReplyRevision, len(history))
	}

	if err := store.RecordExternalReply(opCtx(t), candidate, staffEcho(t, unique(t, "new")), resumedAt.Add(time.Minute), resumedAt.Add(time.Minute)); err != nil {
		t.Fatalf("RecordExternalReply() new echo returned error: %v", err)
	}
	loaded, _ = store.FindByID(opCtx(t), candidate.ID)
	if loaded.State != conversation.StateHumanActive || loaded.ExternalReplyRevision != 2 {
		t.Fatalf("new echo: state = %q revision = %d, want human_active and 2", loaded.State, loaded.ExternalReplyRevision)
	}
}

// TestSaveRejectsACopyLoadedBeforeStaffReplied is the cross-instance guard: a
// service instance that loaded the conversation, then spent seconds waiting on
// the model, must not write its stale copy over a takeover that happened
// meanwhile.
func TestSaveRejectsACopyLoadedBeforeStaffReplied(t *testing.T) {
	store := newStore(t)
	candidate := conv(t)
	candidate.Provider = messaging.ProviderWhatsApp

	if _, err := store.FindOrOpen(opCtx(t), candidate); err != nil {
		t.Fatalf("FindOrOpen() returned error: %v", err)
	}
	stale, err := store.FindByID(opCtx(t), candidate.ID)
	if err != nil {
		t.Fatalf("FindByID() returned error: %v", err)
	}

	if err := store.RecordExternalReply(opCtx(t), candidate, staffEcho(t, unique(t, "wamid")), testNow, testNow); err != nil {
		t.Fatalf("RecordExternalReply() returned error: %v", err)
	}

	stale.LastMessageAt = testNow.Add(time.Second)
	if err := store.Save(opCtx(t), stale); !errors.Is(err, conversation.ErrExternalReplyConflict) {
		t.Fatalf("Save() of a stale copy returned %v, want ErrExternalReplyConflict", err)
	}

	fresh, err := store.FindByID(opCtx(t), candidate.ID)
	if err != nil {
		t.Fatalf("FindByID() returned error: %v", err)
	}
	if fresh.State != conversation.StateHumanActive {
		t.Fatalf("stale save was applied: state = %q", fresh.State)
	}
	fresh.LastMessageAt = testNow.Add(time.Second)
	if err := store.Save(opCtx(t), fresh); err != nil {
		t.Fatalf("Save() of the current copy returned error: %v", err)
	}
}
