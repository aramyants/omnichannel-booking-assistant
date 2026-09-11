package telegram

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestDownloadTelegramAudioResolvesFileAndKeepsOGG(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/bottest-token/getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"voice/file.oga","file_size":9}}`))
		case "/file/bottest-token/voice/file.oga":
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write([]byte("OggS-data"))
		default:
			t.Errorf("unexpected media path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient("test-token", WithBaseURL(srv.URL))
	got, err := c.DownloadAudio(t.Context(), messaging.Audio{Reference: "file-id", MIMEType: "audio/ogg"})
	if err != nil || string(got.Data) != "OggS-data" || got.Filename != "voice.ogg" {
		t.Fatalf("audio = %+v, %v", got, err)
	}
	if _, err := c.DownloadAudio(t.Context(), messaging.Audio{Reference: "large", SizeBytes: messaging.MaxAudioBytes + 1}); err == nil || requests != 2 {
		t.Fatal("oversized audio was downloaded")
	}
}
