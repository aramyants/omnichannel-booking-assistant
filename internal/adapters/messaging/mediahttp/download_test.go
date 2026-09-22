package mediahttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadsRejectRedirectsAndStreamingOversize(t *testing.T) {
	for _, kind := range []string{"redirect", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "redirect" {
					http.Redirect(w, r, "https://example.com/private", http.StatusFound)
					return
				}
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte("too-large"))
			}))
			defer srv.Close()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			if _, _, err := Download(srv.Client(), req, 4); err == nil {
				t.Fatal("unsafe download accepted")
			}
		})
	}
}

func TestTelegramVoiceFileExtensionsWithoutUsefulContentType(t *testing.T) {
	for _, filename := range []string{"voice/note.oga", "voice/note.opus"} {
		t.Run(filename, func(t *testing.T) {
			got, err := Filename(filename, "application/octet-stream")
			if err != nil || got != "voice.ogg" {
				t.Fatalf("Filename(%q) = %q, %v; want voice.ogg", filename, got, err)
			}
		})
	}
}

func TestMetaVoiceDetectsAnExtensionlessMP4Container(t *testing.T) {
	data := append([]byte{0, 0, 0, 24}, []byte("ftypM4A ")...)
	got, err := Filename("/signed-media", "application/octet-stream", data)
	if err != nil || got != "voice.m4a" {
		t.Fatalf("Filename() = %q, %v; want voice.m4a", got, err)
	}
}

func TestKnownMetaAudioContentTypes(t *testing.T) {
	for contentType, want := range map[string]string{
		"video/mp4": "voice.mp4",
	} {
		got, err := Filename("/signed-media", contentType)
		if err != nil || got != want {
			t.Fatalf("Filename(%q) = %q, %v; want %s", contentType, got, err, want)
		}
	}
}

func TestRawAACIsNotRelabelledAsAnM4AContainer(t *testing.T) {
	if _, err := Filename("/signed-media", "audio/aac", []byte{0xff, 0xf1, 0x50, 0x80}); err == nil {
		t.Fatal("raw AAC is not a supported transcription container")
	}
}

func TestUnknownExtensionlessMediaStillFailsClosed(t *testing.T) {
	if _, err := Filename("/signed-media", "application/octet-stream", []byte("not audio")); err == nil {
		t.Fatal("arbitrary bytes were accepted as audio")
	}
}
