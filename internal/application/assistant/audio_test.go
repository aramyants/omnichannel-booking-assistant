package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/conversation"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type audioSender struct {
	fakeSender
	downloads int
}

func (s *audioSender) DownloadAudio(_ context.Context, _ messaging.Audio) (ai.Audio, error) {
	s.downloads++
	return ai.Audio{Data: []byte("voice"), Filename: "voice.ogg"}, nil
}

type speechStub struct {
	text  string
	err   error
	calls int
}

func (s *speechStub) Transcribe(_ context.Context, _ ai.Audio) (string, error) {
	s.calls++
	return s.text, s.err
}

func TestAudioTranscriptBecomesCustomerContextAndReplyStaysText(t *testing.T) {
	sender := &audioSender{}
	speech := &speechStub{text: "Хочу массаж лица"}
	model := &scriptedAI{responses: []ai.Response{textResponse("Какой массаж лица вас интересует?")}}
	svc, store := newAIService(t, model, defaultScheduling(), sender)
	svc.speech = speech
	msg := incoming("voice")
	msg.Content = messaging.Content{Type: messaging.ContentTypeAudio, Audio: &messaging.Audio{Reference: "file-id"}}
	for range 2 {
		if err := svc.Handle(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if speech.calls != 1 || sender.downloads != 1 || len(sender.sent) != 1 {
		t.Fatal("redelivery repeated transcription or reply")
	}
	if got := model.requests[0].Messages[0]; got.Role != ai.RoleUser || got.Text != speech.text {
		t.Fatalf("audio did not become user context: %+v", got)
	}
	conv, _ := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: msg.Provider, ExternalThreadID: msg.ExternalThreadID})
	history, _ := svc.History(t.Context(), conv.ID)
	if history[0].ContentType != messaging.ContentTypeText || history[0].Text != speech.text {
		t.Fatal("transcript was not stored as text")
	}
	if sender.sent[0].Text == "" {
		t.Fatal("no text reply")
	}
}

func TestUnreadableAudioDoesNotGuessOrInvokeBookingModel(t *testing.T) {
	sender := &audioSender{}
	model := &scriptedAI{responses: []ai.Response{textResponse("should not run")}}
	svc, _ := newAIService(t, model, defaultScheduling(), sender)
	svc.speech = &speechStub{err: errors.New("no speech")}
	msg := incoming("unreadable")
	msg.Content = messaging.Content{Type: messaging.ContentTypeAudio, Audio: &messaging.Audio{Reference: "file-id"}}
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 || len(sender.sent) != 1 || sender.sent[0].Text != audioRetry(languageArmenian) {
		t.Fatal("unreadable audio was not handled safely")
	}
}

func TestUnreadableAudioKeepsTheCaptionForTheTranscriptAndTheModel(t *testing.T) {
	sender := &audioSender{}
	model := &scriptedAI{responses: []ai.Response{textResponse("Конечно, в среду в 16:00 есть окно.")}}
	svc, store := newAIService(t, model, defaultScheduling(), sender)
	svc.speech = &speechStub{err: errors.New("no speech")}

	const caption = "Хочу записаться в среду в 16:00"
	voice := incoming("voice-with-caption")
	voice.Content = messaging.Content{Type: messaging.ContentTypeAudio, Text: caption, Audio: &messaging.Audio{Reference: "file-id"}}
	if err := svc.Handle(t.Context(), voice); err != nil {
		t.Fatal(err)
	}

	conv, _ := store.FindOrOpen(t.Context(), conversation.Conversation{Provider: voice.Provider, ExternalThreadID: voice.ExternalThreadID})
	history, _ := svc.History(t.Context(), conv.ID)
	if history[0].ContentType != messaging.ContentTypeUnsupported || history[0].Text != caption {
		t.Fatalf("caption was lost from the transcript: %+v", history[0])
	}

	followUp := incoming("follow-up")
	followUp.Content = messaging.Content{Type: messaging.ContentTypeText, Text: "Есть место?"}
	if err := svc.Handle(t.Context(), followUp); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatalf("expected one model call, got %d", model.calls)
	}
	var sawCaption bool
	for _, m := range model.requests[0].Messages {
		if m.Role == ai.RoleUser && strings.Contains(m.Text, caption) {
			sawCaption = true
		}
	}
	if !sawCaption {
		t.Fatalf("caption never reached the model: %+v", model.requests[0].Messages)
	}
}
