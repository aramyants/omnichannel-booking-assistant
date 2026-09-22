// Package mediahttp bounds downloads without persisting customer audio.
package mediahttp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

// Download deliberately refuses redirects so authenticated downloads cannot
// forward credentials or follow customer-controlled URLs onto another host.
func Download(client *http.Client, req *http.Request, limit int64) ([]byte, string, error) {
	bounded := *client
	bounded.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	// Provider adapters validate the target (Telegram API origin or Meta CDN
	// allowlist) before passing a request here; redirects are disabled above.
	resp, err := bounded.Do(req) //nolint:gosec // G704: target is validated by the provider adapter.
	if err != nil {
		return nil, "", errors.New("could not download audio")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("audio download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, "", errors.New("audio is too large")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", errors.New("could not read audio")
	}
	if int64(len(data)) > limit || len(data) == 0 {
		return nil, "", errors.New("audio is empty or too large")
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func Filename(name, contentType string, data ...[]byte) (string, error) {
	ext := strings.ToLower(path.Ext(name))
	// Telegram commonly stores Ogg/Opus voice notes with .oga or .opus file
	// paths. The metadata and download may both omit a useful Content-Type.
	if ext == ".oga" || ext == ".opus" {
		return "voice.ogg", nil
	}
	for _, allowed := range []string{".flac", ".mp3", ".mp4", ".mpeg", ".mpga", ".m4a", ".ogg", ".wav", ".webm"} {
		if ext == allowed {
			return "voice" + ext, nil
		}
	}
	mimeType, _, _ := mime.ParseMediaType(contentType)
	ext = map[string]string{
		"application/ogg": ".ogg",
		"audio/flac":      ".flac",
		"audio/mp4":       ".m4a",
		"audio/mpeg":      ".mp3",
		"audio/ogg":       ".ogg",
		"audio/opus":      ".ogg",
		"audio/wav":       ".wav",
		"audio/webm":      ".webm",
		"audio/x-m4a":     ".m4a",
		"audio/x-wav":     ".wav",
		"video/mp4":       ".mp4",
		"video/webm":      ".webm",
	}[mimeType]
	if ext == "" && len(data) > 0 {
		ext = audioExtension(data[0])
	}
	if ext == "" {
		return "", errors.New("unsupported audio format")
	}
	return "voice" + ext, nil
}

// audioExtension recognises the containers used by voice notes when a signed
// provider CDN URL has no extension and responds with application/octet-stream.
// It deliberately accepts only well-known signatures; arbitrary downloaded
// bytes must not be relabelled and sent to the transcription provider.
func audioExtension(data []byte) string {
	switch {
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS")):
		return ".ogg"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("fLaC")):
		return ".flac"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		return ".wav"
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return ".webm"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("ID3")):
		return ".mp3"
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xf6 == 0xf0:
		// ADTS is raw AAC, not an M4A/MP4 container accepted by the
		// transcription endpoint. Renaming it would only postpone the error.
		return ""
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0:
		return ".mp3"
	case len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")):
		return ".m4a"
	default:
		return ""
	}
}

func Validate(audio messaging.Audio) error {
	if audio.Reference == "" {
		return errors.New("audio reference is missing")
	}
	if audio.SizeBytes > messaging.MaxAudioBytes || audio.DurationSeconds > 300 {
		return errors.New("send audio under five minutes and 20 MB")
	}
	return nil
}
