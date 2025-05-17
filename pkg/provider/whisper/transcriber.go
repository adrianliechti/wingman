package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Transcriber = (*Transcriber)(nil)

type Transcriber struct {
	*Config

	model string
}

func NewTranscriber(url, model string, options ...Option) (*Transcriber, error) {
	if url == "" {
		return nil, errors.New("invalid url")
	}

	cfg := &Config{
		client: http.DefaultClient,

		url: url,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Transcriber{
		Config: cfg,
	}, nil
}

func (t *Transcriber) Transcribe(ctx context.Context, input provider.File, options *provider.TranscribeOptions) (*provider.Transcription, error) {
	if options == nil {
		options = new(provider.TranscribeOptions)
	}

	id := uuid.NewString()

	url, _ := url.JoinPath(t.url, "/inference")

	if options.Language == "" {
		options.Language = "auto"
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("id", id)
	w.WriteField("language", options.Language)
	w.WriteField("response_format", "verbose_json")

	file, err := w.CreateFormFile("file", input.Name)

	if err != nil {
		return nil, err
	}

	if _, err := file.Write(input.Content); err != nil {
		return nil, err
	}

	w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", url, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var inference InferenceResponse

	if err := json.NewDecoder(resp.Body).Decode(&inference); err != nil {
		return nil, err
	}

	content := strings.TrimSpace(inference.Text)

	if strings.EqualFold(content, "[BLANK_AUDIO]") {
		content = ""
	}

	result := provider.Transcription{
		ID:    id,
		Model: t.model,

		Text: content,

		// Language: inference.Language,
		// Duration: inference.Duration,
	}

	return &result, nil
}

type InferenceResponse struct {
	Task string `json:"task"`

	Language string  `json:"language"`
	Duration float64 `json:"duration"`

	Text string `json:"text"`
}
