package azurespeech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Transcriber = (*Transcriber)(nil)

type Transcriber struct {
	*Config
}

func NewTranscriber(region, model string, options ...Option) (*Transcriber, error) {
	if region == "" {
		return nil, fmt.Errorf("azure speech region is required (e.g. eastus)")
	}

	cfg := &Config{
		region: region,
		model:  model,

		client: http.DefaultClient,
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

	locale := options.Language
	if locale == "" {
		locale = "en-US"
	}

	definition := transcriptionDefinition{
		Locales: []string{locale},
	}

	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Write audio part
	audioHeader := make(textproto.MIMEHeader)
	audioHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="audio"; filename="%s"`, input.Name))

	if input.ContentType != "" {
		audioHeader.Set("Content-Type", input.ContentType)
	} else {
		audioHeader.Set("Content-Type", "application/octet-stream")
	}

	audioPart, err := writer.CreatePart(audioHeader)
	if err != nil {
		return nil, err
	}

	if _, err := audioPart.Write(input.Content); err != nil {
		return nil, err
	}

	// Write definition part
	definitionHeader := make(textproto.MIMEHeader)
	definitionHeader.Set("Content-Disposition", `form-data; name="definition"`)
	definitionHeader.Set("Content-Type", "application/json")

	definitionPart, err := writer.CreatePart(definitionHeader)
	if err != nil {
		return nil, err
	}

	if _, err := definitionPart.Write(definitionJSON); err != nil {
		return nil, err
	}

	writer.Close()

	endpoint := t.sttURL() + "/speechtotext/transcriptions:transcribe?api-version=2025-10-15"

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if t.token != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", t.token)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result transcriptionResponse

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	var text string

	for _, p := range result.CombinedPhrases {
		if text != "" {
			text += " "
		}

		text += p.Text
	}

	return &provider.Transcription{
		ID:    uuid.NewString(),
		Model: t.model,

		Text: text,
	}, nil
}

type transcriptionDefinition struct {
	Locales []string `json:"locales,omitempty"`
}

type transcriptionResponse struct {
	DurationMilliseconds int `json:"durationMilliseconds"`

	CombinedPhrases []struct {
		Text string `json:"text"`
	} `json:"combinedPhrases"`

	Phrases []struct {
		Text                 string  `json:"text"`
		Confidence           float64 `json:"confidence"`
		Locale               string  `json:"locale"`
		OffsetMilliseconds   int     `json:"offsetMilliseconds"`
		DurationMilliseconds int     `json:"durationMilliseconds"`
	} `json:"phrases"`
}
