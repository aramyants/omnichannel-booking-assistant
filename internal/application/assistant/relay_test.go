package assistant

import (
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/persistence/memory"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
)

// handedOver drives a conversation to the state a colleague finds it in.
func handedOver(t *testing.T) (*Service, *memory.Store, *fakeSender, conversation.Conversation) {
	t.Helper()

	sender := &fakeSender{}
	model := &scriptedAI{responses: []ai.Response{
		toolResponse("call_1", toolRequestHandoff, `{"reason":"wants a person"}`),
		textResponse("A colleague will reply shortly."),
	}}
	svc, store := newAIServiceWithStaff(t, model, defaultScheduling(), sender, &recordingStaff{})

	if err := svc.Handle(t.Context(), incoming("4127")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	conv := openConversation(t, store)
	if conv.State != conversation.StateHumanRequested {
		t.Fatalf("state = %q, want the conversation waiting for a person", conv.State)
	}
	return svc, store, sender, conv
}

// TestAColleagueReplyReachesTheCustomer is the whole point of the relay: a
// manager answers in the staff chat and the customer hears from them, without
// leaving Telegram or starting a separate conversation.
func TestAColleagueReplyReachesTheCustomer(t *testing.T) {
	svc, store, sender, conv := handedOver(t)
	before := len(sender.sent)

	if err := svc.RelayStaffReply(t.Context(), StaffReply{
		ConversationID: conv.ID,
		AuthorName:     "Garik",
		Text:           "Բարև, ես եմ",
	}); err != nil {
		t.Fatalf("RelayStaffReply() returned error: %v", err)
	}

	if len(sender.sent) != before+1 {
		t.Fatalf("the customer got %d messages, want one more", len(sender.sent))
	}

	delivered := sender.sent[len(sender.sent)-1]
	if delivered.ExternalThreadID != "219847362" {
		t.Errorf("delivered to %q, want the customer's thread", delivered.ExternalThreadID)
	}

	// Signed, so the customer knows a person has taken over. They asked for
	// one, and hiding it would be a lie the business has to keep up.
	if !strings.HasPrefix(delivered.Text, "Garik: ") {
		t.Errorf("message = %q, want it signed by the colleague", delivered.Text)
	}

	// A person answering has taken the conversation, whether or not they said so.
	if got := openConversation(t, store); got.State != conversation.StateHumanActive {
		t.Errorf("state = %q, want human_active", got.State)
	}
}

// TestAColleagueReplyIsRecorded: the transcript is what was said to this
// customer, whoever said it. Without this the assistant would later resume with
// no idea what a colleague had already promised.
func TestAColleagueReplyIsRecorded(t *testing.T) {
	svc, _, _, conv := handedOver(t)

	if err := svc.RelayStaffReply(t.Context(), StaffReply{
		ConversationID: conv.ID,
		AuthorName:     "Garik",
		Text:           "I can offer you Friday at two.",
	}); err != nil {
		t.Fatalf("RelayStaffReply() returned error: %v", err)
	}

	history, err := svc.History(t.Context(), conv.ID)
	if err != nil {
		t.Fatalf("History() returned error: %v", err)
	}

	var found bool
	for _, msg := range history {
		if strings.Contains(msg.Text, "Friday at two") {
			found = true
			if msg.Direction != conversation.DirectionOutbound {
				t.Errorf("direction = %q, want outbound", msg.Direction)
			}
		}
	}
	if !found {
		t.Error("the colleague's message is missing from the transcript")
	}
}

// TestTheAssistantStaysQuietWhileAColleagueIsTalking guards the obvious way to
// get a relay wrong: two voices answering the same customer at once.
func TestTheAssistantStaysQuietWhileAColleagueIsTalking(t *testing.T) {
	svc, _, sender, conv := handedOver(t)

	if err := svc.RelayStaffReply(t.Context(), StaffReply{
		ConversationID: conv.ID, AuthorName: "Garik", Text: "One moment.",
	}); err != nil {
		t.Fatalf("RelayStaffReply() returned error: %v", err)
	}
	count := len(sender.sent)

	if err := svc.Handle(t.Context(), incoming("4128")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(sender.sent) != count {
		t.Errorf("the assistant answered over a colleague: %+v", sender.sent[count:])
	}
}

// TestResumeHandsBackAfterAColleagueHasFinished covers the end of a handover.
func TestResumeHandsBackAfterAColleagueHasFinished(t *testing.T) {
	svc, store, sender, conv := handedOver(t)

	if err := svc.RelayStaffReply(t.Context(), StaffReply{
		ConversationID: conv.ID, AuthorName: "Garik", Text: "All sorted.",
	}); err != nil {
		t.Fatalf("RelayStaffReply() returned error: %v", err)
	}

	answer, err := svc.RunStaffCommand(t.Context(), CommandResume, conv.ID)
	if err != nil {
		t.Fatalf("RunStaffCommand() returned error: %v", err)
	}
	if !strings.Contains(strings.ToLower(answer), "assistant") {
		t.Errorf("answer = %q, want it to say the assistant is back", answer)
	}
	if got := openConversation(t, store); got.State != conversation.StateAssistantActive {
		t.Fatalf("state = %q, want assistant_active", got.State)
	}

	count := len(sender.sent)
	if err := svc.Handle(t.Context(), incoming("4129")); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if len(sender.sent) != count+1 {
		t.Error("the assistant did not answer after being handed back")
	}
}

func TestStaffCommandsAreValidated(t *testing.T) {
	svc, _, _, conv := handedOver(t)

	if _, err := svc.RunStaffCommand(t.Context(), StaffCommand("evaporate"), conv.ID); err == nil {
		t.Error("RunStaffCommand() accepted a command that does not exist")
	}
	if _, err := svc.RunStaffCommand(t.Context(), CommandResume, "no-such-conversation"); err == nil {
		t.Error("RunStaffCommand() accepted a conversation that does not exist")
	}
}

func TestRelayRejectsIncompleteReplies(t *testing.T) {
	svc, _, sender, conv := handedOver(t)
	before := len(sender.sent)

	for name, reply := range map[string]StaffReply{
		"no conversation": {AuthorName: "Garik", Text: "hello"},
		"no text":         {ConversationID: conv.ID, AuthorName: "Garik", Text: "   "},
	} {
		t.Run(name, func(t *testing.T) {
			if err := svc.RelayStaffReply(t.Context(), reply); err == nil {
				t.Error("RelayStaffReply() accepted an incomplete reply")
			}
		})
	}

	if len(sender.sent) != before {
		t.Error("an incomplete reply reached the customer")
	}
}

// TestUnsignedRepliesStillReach covers a channel that gives no author name.
func TestUnsignedRepliesStillReach(t *testing.T) {
	svc, _, sender, conv := handedOver(t)

	if err := svc.RelayStaffReply(t.Context(), StaffReply{
		ConversationID: conv.ID, Text: "We are open until six.",
	}); err != nil {
		t.Fatalf("RelayStaffReply() returned error: %v", err)
	}

	if got := sender.sent[len(sender.sent)-1].Text; got != "We are open until six." {
		t.Errorf("message = %q, want it delivered unsigned", got)
	}
}
