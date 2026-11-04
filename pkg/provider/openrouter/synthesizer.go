package openrouter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"iter"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(model string, options ...Option) (*Synthesizer, error) {
	return &Synthesizer{
		Config: newConfig(model, options...),
	}, nil
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

	body := map[string]any{
		"model": s.model,
		"input": content,
	}

	if options.Voice != "" {
		body["voice"] = options.Voice
	}

	if options.Speed != nil {
		body["speed"] = *options.Speed
	}

	if options.Format != "" {
		body["response_format"] = options.Format
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
		body, _ := io.ReadAll(resp.Body)

		return nil, &provider.ProviderError{
			Code:    resp.StatusCode,
			Message: string(body),
		}
	}

	audio, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	contentType := "audio/mpeg"

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		contentType = ct
	}

	return &provider.Synthesis{
		ID:    uuid.NewString(),
		Model: s.model,

		Content:     audio,
		ContentType: contentType,
	}, nil
}
