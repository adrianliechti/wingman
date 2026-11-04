package xai

import (
	"bytes"
	"context"
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
		url:   "https://api.x.ai/v1",
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

type ttsRequest struct {
	Text         string           `json:"text"`
	VoiceID      string           `json:"voice_id,omitzero"`
	Language     string           `json:"language,omitzero"`
	OutputFormat *ttsOutputFormat `json:"output_format,omitzero"`
}

type ttsOutputFormat struct {
	Codec      string `json:"codec,omitzero"`
	SampleRate int    `json:"sample_rate,omitzero"`
	BitRate    int    `json:"bit_rate,omitzero"`
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

	voice := mapVoice(options.Voice)

	codec, contentType := mapFormat(options.Format)

	body := ttsRequest{
		Text:     content,
		VoiceID:  strings.ToLower(voice),
		Language: "auto",
	}

	if codec != "" {
		body.OutputFormat = &ttsOutputFormat{
			Codec:      codec,
			SampleRate: 24000,
		}

		if codec == "mp3" {
			body.OutputFormat.BitRate = 128000
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/tts", bytes.NewReader(data))
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

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &provider.Synthesis{
		ID:    uuid.NewString(),
		Model: s.model,

		Content:     audioData,
		ContentType: contentType,
	}, nil
}

var voiceMap = map[string]string{
	"alloy":   "eve",
	"ash":     "rex",
	"ballad":  "sal",
	"coral":   "ara",
	"echo":    "leo",
	"fable":   "eve",
	"nova":    "ara",
	"onyx":    "leo",
	"sage":    "sal",
	"shimmer": "eve",
	"verse":   "rex",
	"marin":   "ara",
	"cedar":   "rex",
}

func mapVoice(voice string) string {
	if voice == "" {
		return "eve"
	}

	if mapped, ok := voiceMap[strings.ToLower(voice)]; ok {
		return mapped
	}

	return strings.ToLower(voice)
}

// mapFormat maps OpenAI response_format values to xAI codec and HTTP content type.
func mapFormat(format string) (codec string, contentType string) {
	switch strings.ToLower(format) {
	case "mp3":
		return "mp3", "audio/mpeg"
	case "wav":
		return "wav", "audio/wav"
	case "pcm":
		return "pcm", "audio/pcm"
	case "opus":
		return "mp3", "audio/mpeg"
	case "aac":
		return "mp3", "audio/mpeg"
	case "flac":
		return "wav", "audio/wav"
	default:
		return "mp3", "audio/mpeg"
	}
}
