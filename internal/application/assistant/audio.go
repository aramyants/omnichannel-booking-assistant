package assistant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type AudioDownloader interface {
	DownloadAudio(context.Context, messaging.Audio) (ai.Audio, error)
}

func (s *Service) transcribeInput(ctx context.Context, msg messaging.Envelope) (messaging.Envelope, error) {
	if s.speech == nil || msg.Content.Audio == nil {
		return msg, errors.New("audio transcription is unavailable")
	}
	source, ok := s.senders[msg.Provider].(AudioDownloader)
	if !ok {
		return msg, errors.New("audio download is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	audio, err := source.DownloadAudio(ctx, *msg.Content.Audio)
	if err != nil {
		return msg, err
	}
	text, err := s.speech.Transcribe(ctx, audio)
	if err != nil {
		return msg, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return msg, errors.New("no speech recognized")
	}
	if caption := strings.TrimSpace(msg.Content.Text); caption != "" {
		text = caption + "\n" + text
	}
	// Only the transcript enters history/model context. Raw audio is ephemeral.
	msg.Content = messaging.Content{Type: messaging.ContentTypeText, Text: text, Description: "transcribed voice message"}
	return msg, nil
}

func audioRetry(lang language) string {
	switch lang {
	case languageArmenian:
		return "Չկարողացա հասկանալ ձայնային հաղորդագրությունը։ Խնդրում եմ ուղարկեք ավելի կարճ ձայնային հաղորդագրություն կամ գրեք Ձեր հարցը։"
	case languageRussian:
		return "Не удалось разобрать голосовое сообщение. Отправьте, пожалуйста, более короткое аудио или напишите свой вопрос."
	default:
		return "I could not understand that audio. Please send a shorter voice message or type your request."
	}
}
