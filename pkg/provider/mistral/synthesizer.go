package mistral

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(model string, options ...Option) (*Synthesizer, error) {
	cfg := &Config{
		url:   "https://api.mistral.ai/v1",
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	if cfg.client == nil {
		cfg.client = provider.DefaultClient
	}

	return &Synthesizer{
		Config: cfg,
	}, nil
}

type speechRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`

	VoiceID        string `json:"voice_id,omitzero"`
	ResponseFormat string `json:"response_format,omitzero"`
}

type speechResponse struct {
	AudioData string `json:"audio_data"`
}

func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) iter.Seq2[*provider.Synthesis, error] {
	return func(yield func(*provider.Synthesis, error) bool) {
		synthesis, err := s.synthesize(ctx, content, options)

		if err != nil {
			yield(nil, err)
			return
		}

		yield(synthesis, nil)
	}
}

func (s *Synthesizer) synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	if options == nil {
		options = new(provider.SynthesizeOptions)
	}

	voice := "en_paul_neutral"

	if options.Voice != "" {
		voice = options.Voice
	}

	format, contentType := mapFormat(options.Format)

	body := speechRequest{
		Model: s.model,
		Input: content,

		VoiceID:        voice,
		ResponseFormat: format,
	}

	data, err := json.Marshal(body)

	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/audio/speech", bytes.NewReader(data))

	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)

		return nil, &provider.ProviderError{
			Code:    resp.StatusCode,
			Message: string(errBody),
		}
	}

	var result speechResponse

	if err := json.UnmarshalRead(resp.Body, &result); err != nil {
		return nil, err
	}

	audio, err := base64.StdEncoding.DecodeString(result.AudioData)

	if err != nil {
		return nil, err
	}

	return &provider.Synthesis{
		ID:    uuid.NewString(),
		Model: s.model,

		Content:     audio,
		ContentType: contentType,
	}, nil
}

// mapFormat maps OpenAI response_format values to Mistral formats and HTTP content types.
func mapFormat(format string) (string, string) {
	switch strings.ToLower(format) {
	case "wav":
		return "wav", "audio/wav"
	case "pcm":
		return "pcm", "audio/pcm"
	case "flac":
		return "flac", "audio/flac"
	case "opus":
		return "opus", "audio/opus"
	default:
		return "mp3", "audio/mpeg"
	}
}
