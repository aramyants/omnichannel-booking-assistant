package meta

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

type audioTransport func(*http.Request) (*http.Response, error)

func (f audioTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWhatsAppAudioMetadataAndDownloadAreAuthenticated(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: audioTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing media authorization")
		}
		body, mimeType := `{"url":"https://lookaside.fbsbx.com/audio","mime_type":"audio/ogg","file_size":9}`, "application/json"
		if calls == 2 {
			if r.URL.Host != "lookaside.fbsbx.com" {
				t.Error("wrong media host")
			}
			body, mimeType = "OggS-data", "audio/ogg"
		} else if r.URL.Path != "/v22.0/media-1" {
			t.Error("wrong metadata request")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{mimeType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	c, err := NewClient("test-token", "number", WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	audio, err := c.DownloadAudio(t.Context(), messaging.Audio{Reference: "media-1"})
	if err != nil || calls != 2 || string(audio.Data) != "OggS-data" || audio.Filename != "voice.ogg" {
		t.Fatalf("audio download failed: %+v %v", audio, err)
	}
}

func TestMetaMediaURLsCannotFetchArbitraryServers(t *testing.T) {
	for _, raw := range []string{"http://lookaside.fbsbx.com/file", "https://127.0.0.1/file", "https://fbcdn.net.evil.example/file", "https://example.com/file", "https://user:password@lookaside.fbsbx.com/file", "https://lookaside.fbsbx.com:8080/file"} {
		if trustedMediaURL(raw) {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
	if !trustedMediaURL("https://scontent.cdninstagram.com/audio.mp4?signature=value") {
		t.Fatal("valid media CDN rejected")
	}
}

func TestSocialAudioIsDownloadedWithoutSendingPageTokenToCDN(t *testing.T) {
	for _, provider := range []messaging.Provider{messaging.ProviderInstagram, messaging.ProviderMessenger} {
		t.Run(string(provider), func(t *testing.T) {
			event := strings.Replace(directEvent, `"text":"Можно на русском?"`, `"attachments":[{"type":"audio","payload":{"url":"https://scontent.cdninstagram.com/voice.mp4"}}]`, 1)
			object := "instagram"
			if provider == messaging.ProviderMessenger {
				object = "page"
			}
			messages, err := parseDirect(directUpdate(object, "business-1", event), receivedAt, provider, "business-1")
			if err != nil || len(messages) != 1 || messages[0].Content.Type != messaging.ContentTypeAudio {
				t.Fatalf("audio was not recognized: %+v %v", messages, err)
			}
			client, err := newDirectClient("private-token", "business-1", provider, WithHTTPClient(&http.Client{Transport: audioTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "" {
					t.Error("Page/Instagram token sent to CDN")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"audio/mp4"}}, Body: io.NopCloser(strings.NewReader("audio"))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.DownloadAudio(t.Context(), *messages[0].Content.Audio); err != nil {
				t.Fatal(err)
			}
		})
	}
}
