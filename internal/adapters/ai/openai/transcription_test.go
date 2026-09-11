package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
)

func TestTranscriptionUploadsAudioAndReturnsOnlyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("invalid transcription request")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		if r.FormValue("model") != "gpt-4o-mini-transcribe" || r.FormValue("response_format") != "json" || r.FormValue("language") != "" {
			t.Error("invalid transcription settings")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = file.Close() }()
		data, _ := io.ReadAll(file)
		if string(data) != "OggS-audio" || header.Filename != "voice.ogg" {
			t.Error("audio upload was changed")
		}
		_, _ = w.Write([]byte(`{"text":"  Хочу массаж лица  "}`))
	}))
	defer srv.Close()
	text, err := testClient(t, srv).Transcribe(t.Context(), ai.Audio{Data: []byte("OggS-audio"), Filename: "voice.ogg"})
	if err != nil || text != "Хочу массаж лица" {
		t.Fatalf("transcript = %q, %v", text, err)
	}
}

func TestTranscriptionRejectsEmptySpeechAndProviderErrors(t *testing.T) {
	for _, body := range []string{`{"text":""}`, `{"error":"unavailable"}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := testClient(t, srv).Transcribe(t.Context(), ai.Audio{Data: []byte("audio"), Filename: "voice.ogg"}); err == nil {
				t.Fatal("unreadable audio accepted")
			}
		})
	}
}
