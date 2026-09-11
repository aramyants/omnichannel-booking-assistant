// Package mediahttp bounds downloads without persisting customer audio.
package mediahttp

import (
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

func Filename(name, contentType string) (string, error) {
	ext := strings.ToLower(path.Ext(name))
	for _, allowed := range []string{".flac", ".mp3", ".mp4", ".mpeg", ".mpga", ".m4a", ".ogg", ".wav", ".webm"} {
		if ext == allowed {
			return "voice" + ext, nil
		}
	}
	mimeType, _, _ := mime.ParseMediaType(contentType)
	ext = map[string]string{"audio/ogg": ".ogg", "audio/opus": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/webm": ".webm", "video/webm": ".webm", "audio/flac": ".flac"}[mimeType]
	if ext == "" {
		return "", errors.New("unsupported audio format")
	}
	return "voice" + ext, nil
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
