package client

import (
	"context"
	"io"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type TranscriptionService struct {
	Options []RequestOption
}

func NewTranscriptionService(opts ...RequestOption) TranscriptionService {
	return TranscriptionService{
		Options: opts,
	}
}

type Transcription = provider.Transcription
type TranscribeOptions = provider.TranscribeOptions

type TranscribeRequest struct {
	TranscribeOptions

	Model string

	Name   string
	Reader io.Reader
}

func (r *TranscriptionService) New(ctx context.Context, input TranscribeRequest, opts ...RequestOption) (*Transcription, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)
	url := strings.TrimRight(cfg.URL, "/") + "/v1/"

	options := []openai.Option{}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Client != nil {
		options = append(options, openai.WithClient(cfg.Client))
	}

	p, err := openai.NewTranscriber(url, input.Model, options...)

	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(input.Reader)

	if err != nil {
		return nil, err
	}

	file := provider.File{
		Name:    input.Name,
		Content: data,
	}

	return p.Transcribe(ctx, file, &input.TranscribeOptions)
}
