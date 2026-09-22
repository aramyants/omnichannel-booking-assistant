package meta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/mediahttp"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (c *Client) DownloadAudio(ctx context.Context, audio messaging.Audio) (ai.Audio, error) {
	if err := mediahttp.Validate(audio); err != nil {
		return ai.Audio{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+c.version+"/"+url.PathEscape(audio.Reference), nil)
	if err != nil {
		return ai.Audio{}, errors.New("invalid media reference")
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	raw, _, err := mediahttp.Download(c.httpClient, req, 64<<10)
	if err != nil {
		return ai.Audio{}, err
	}
	var media struct {
		URL      string `json:"url"`
		MIMEType string `json:"mime_type"`
		Size     int64  `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &media); err != nil {
		return ai.Audio{}, errors.New("invalid media metadata")
	}
	if media.Size > messaging.MaxAudioBytes {
		return ai.Audio{}, errors.New("audio is too large")
	}
	return downloadMetaAudio(ctx, c.httpClient, media.URL, media.MIMEType, c.accessToken)
}

func (c *DirectClient) DownloadAudio(ctx context.Context, audio messaging.Audio) (ai.Audio, error) {
	if err := mediahttp.Validate(audio); err != nil {
		return ai.Audio{}, err
	}
	return downloadMetaAudio(ctx, c.client.httpClient, audio.Reference, audio.MIMEType, "")
}

func trustedMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"fbcdn.net", "cdninstagram.com", "fbsbx.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func downloadMetaAudio(ctx context.Context, client *http.Client, rawURL, mimeType, token string) (ai.Audio, error) {
	if !trustedMediaURL(rawURL) {
		return ai.Audio{}, errors.New("audio URL is not a Meta media host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ai.Audio{}, errors.New("invalid media URL")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	data, detected, err := mediahttp.Download(client, req, messaging.MaxAudioBytes)
	if err != nil {
		return ai.Audio{}, err
	}
	if mimeType == "" {
		mimeType = detected
	}
	name, err := mediahttp.Filename(req.URL.Path, mimeType, data)
	return ai.Audio{Data: data, Filename: name}, err
}
