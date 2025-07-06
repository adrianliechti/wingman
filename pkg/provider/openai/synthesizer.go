package openai

import (
	"context"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
	"github.com/openai/openai-go"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
	speech openai.AudioSpeechService
}

func NewSynthesizer(url, model string, options ...Option) (*Synthesizer, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Synthesizer{
		Config: cfg,
		speech: openai.NewAudioSpeechService(cfg.Options()...),
	}, nil
}

func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	if options == nil {
		options = new(provider.SynthesizeOptions)
	}

	params := openai.AudioSpeechNewParams{
		Model: s.model,
		Input: content,

		Voice: openai.AudioSpeechNewParamsVoiceAlloy,
	}

	if options.Voice != "" {
		params.Voice = openai.AudioSpeechNewParamsVoice(options.Voice)
	}

	if options.Speed != nil {
		params.Speed = openai.Float(float64(*options.Speed))
	}

	if options.Format != "" {
		params.ResponseFormat = openai.AudioSpeechNewParamsResponseFormat(options.Format)
	}

	result, err := s.speech.New(ctx, params)

	if err != nil {
		return nil, convertError(err)
	}

	data, err := io.ReadAll(result.Body)

	if err != nil {
		return nil, err
	}

	return &provider.Synthesis{
		ID:    uuid.NewString(),
		Model: s.model,

		Content:     data,
		ContentType: "audio/mpeg",
	}, nil
}
