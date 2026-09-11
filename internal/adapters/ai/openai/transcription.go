package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/ai"
	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func (c *Client) Transcribe(ctx context.Context, audio ai.Audio) (string, error) {
	if len(audio.Data) == 0 || len(audio.Data) > messaging.MaxAudioBytes {
		return "", errors.New("audio is empty or too large")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", path.Base(audio.Filename))
	if err != nil {
		return "", err
	}
	if _, err := file.Write(audio.Data); err != nil {
		return "", err
	}
	if err := form.WriteField("model", c.transcriptionModel); err != nil {
		return "", err
	}
	if err := form.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	// Let the recognizer detect the spoken language; the app's UI language may
	// differ. Transcribe rather than translate, preserving the customer's intent.
	if err := form.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription: %w", ai.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("transcription: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", errors.New("could not read transcription")
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("invalid transcription response")
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return "", errors.New("no speech recognized")
	}
	return text, nil
}
