package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/adapters/messaging/mediahttp"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (c *Client) DownloadAudio(ctx context.Context, audio messaging.Audio) (ai.Audio, error) {
	if err := mediahttp.Validate(audio); err != nil {
		return ai.Audio{}, err
	}
	raw, err := c.call(ctx, "getFile", map[string]string{"file_id": audio.Reference})
	if err != nil {
		return ai.Audio{}, err
	}
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return ai.Audio{}, errors.New("invalid audio metadata")
	}
	if file.Path == "" || strings.Contains(file.Path, "..") || strings.ContainsAny(file.Path, "?#\\") || strings.HasPrefix(file.Path, "/") || file.Size > messaging.MaxAudioBytes {
		return ai.Audio{}, errors.New("invalid audio path or size")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+file.Path, nil)
	if err != nil {
		return ai.Audio{}, c.redact(err)
	}
	data, mimeType, err := mediahttp.Download(c.httpClient, req, messaging.MaxAudioBytes)
	if err != nil {
		return ai.Audio{}, err
	}
	if audio.MIMEType != "" {
		mimeType = audio.MIMEType
	}
	name, err := mediahttp.Filename(file.Path, mimeType)
	return ai.Audio{Data: data, Filename: name}, err
}
