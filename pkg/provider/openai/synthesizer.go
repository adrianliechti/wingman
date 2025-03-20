package openai

import (
	"context"

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

	result, err := s.speech.New(ctx, openai.AudioSpeechNewParams{
		Model: s.model,
		Input: content,

		Voice: openai.AudioSpeechNewParamsVoiceAlloy,

		ResponseFormat: openai.AudioSpeechNewParamsResponseFormatWAV,
	})

	if err != nil {
		return nil, convertError(err)
	}

	id := uuid.NewString()

	return &provider.Synthesis{
		ID: id,

		Name:   id + ".wav",
		Reader: result.Body,
	}, nil
}
