package assistant

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/ai/openai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Opt-in model evaluation uses a fake calendar/customer and a recording sender.
// It creates no appointments and sends no messages to messaging platforms.
func TestLiveCategoryReplies(t *testing.T) {
	if os.Getenv("ASSISTANT_AI_LIVE") != "1" {
		t.Skip("set ASSISTANT_AI_LIVE=1 to evaluate the configured model")
	}
	client, err := openai.NewClient(os.Getenv("OPENAI_API_KEY"), openai.WithModel(os.Getenv("OPENAI_MODEL")))
	if err != nil {
		t.Fatal(err)
	}
	for name, question := range map[string]string{
		"English":  "What face massage services and prices do you offer?",
		"Russian":  "Какие у вас есть массажи лица и сколько стоят?",
		"Armenian": "Դեմքի մերսման ի՞նչ ծառայություններ ունեք և ի՞նչ գներով։",
	} {
		t.Run(name, func(t *testing.T) {
			sender := &fakeSender{}
			svc, _ := newAIService(t, client, categoryCalendar(), sender)
			if err := svc.Handle(t.Context(), incomingText("category-evaluation", question)); err != nil {
				t.Fatal(err)
			}
			if len(sender.sent) != 1 {
				t.Fatalf("received %d replies", len(sender.sent))
			}
			reply := sender.sent[0]
			text := strings.ToLower(reply.Text)
			if !strings.Contains(text, "face motion") || !strings.Contains(text, "guasha") || strings.Contains(text, "motion sport") || strings.Contains(text, "motion relax") {
				t.Fatalf("category request failed: %s", reply.Text)
			}
			for _, choice := range reply.Choices {
				if choice.Label != "Face motion" && choice.Label != "Face Motion Guasha" {
					t.Fatalf("unrelated choice: %q", choice.Label)
				}
			}
			t.Logf("%s: %s; choices=%v", name, reply.Text, labelsOf(reply.Choices))
		})
	}
}

func TestLiveVoiceUnderstanding(t *testing.T) {
	if os.Getenv("ASSISTANT_AI_LIVE") != "1" || os.Getenv("ASSISTANT_TEST_AUDIO") == "" {
		t.Skip("opt-in synthetic voice evaluation")
	}
	data, err := os.ReadFile(os.Getenv("ASSISTANT_TEST_AUDIO"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := openai.NewClient(os.Getenv("OPENAI_API_KEY"), openai.WithModel(os.Getenv("OPENAI_MODEL")))
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordedAudioSender{data: data}
	svc, _ := newAIService(t, client, categoryCalendar(), sender)
	svc.speech = client
	msg := incoming("voice-evaluation")
	msg.Content = messaging.Content{Type: messaging.ContentTypeAudio, Audio: &messaging.Audio{Reference: "synthetic-test"}}
	if err := svc.Handle(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || !strings.Contains(strings.ToLower(sender.sent[0].Text), "face motion") || strings.Contains(strings.ToLower(sender.sent[0].Text), "motion sport") {
		t.Fatalf("voice category request failed: %+v", sender.sent)
	}
	t.Logf("Text reply to synthetic speech: %s", sender.sent[0].Text)
}

type recordedAudioSender struct {
	fakeSender
	data []byte
}

func (s *recordedAudioSender) DownloadAudio(_ context.Context, _ messaging.Audio) (ai.Audio, error) {
	return ai.Audio{Data: s.data, Filename: "voice.wav"}, nil
}
