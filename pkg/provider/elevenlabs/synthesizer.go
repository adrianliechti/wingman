package elevenlabs

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

var _ provider.Synthesizer = (*Synthesizer)(nil)

type Synthesizer struct {
	*Config
}

func NewSynthesizer(url, model string, options ...Option) (*Synthesizer, error) {
	cfg := &Config{
		client: http.DefaultClient,

		url:   "https://api.elevenlabs.io",
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Synthesizer{
		Config: cfg,
	}, nil
}

func (s *Synthesizer) Synthesize(ctx context.Context, content string, options *provider.SynthesizeOptions) (*provider.Synthesis, error) {
	if options == nil {
		options = new(provider.SynthesizeOptions)
	}

	u, _ := url.Parse(strings.TrimRight(s.url, "/") + "/v1/text-to-speech/" + s.model)

	body := map[string]any{
		"text":     content,
		"model_id": "eleven_multilingual_v2",
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", u.String(), jsonReader(body))
	req.Header.Set("xi-api-key", s.token)

	resp, err := s.client.Do(req)

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	id := uuid.NewString()

	return &provider.Synthesis{
		ID: id,

		Content:     data,
		ContentType: "audio/mpeg",
	}, nil
}
