package client

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
)

type CompletionService struct {
	Options []RequestOption
}

func NewCompletionService(opts ...RequestOption) CompletionService {
	return CompletionService{
		Options: opts,
	}
}

type Message = provider.Message

type Completion = provider.Completion
type CompletionFormat = provider.CompletionFormat
type CompletionStreamHandler = provider.StreamHandler
type CompletionReasoningEffort = provider.ReasoningEffort

type Tool = provider.Tool
type Schema = provider.Schema

type CompletionRequest struct {
	Model    string
	Messages []Message

	provider.CompleteOptions
}

func (r *CompletionService) New(ctx context.Context, input CompletionRequest, opts ...RequestOption) (*Completion, error) {
	cfg := newRequestConfig(append(r.Options, opts...)...)
	url := strings.TrimRight(cfg.URL, "/") + "/v1/"

	options := []openai.Option{}

	if cfg.Token != "" {
		options = append(options, openai.WithToken(cfg.Token))
	}

	if cfg.Client != nil {
		options = append(options, openai.WithClient(cfg.Client))
	}

	p, err := openai.NewCompleter(url, input.Model, options...)

	if err != nil {
		return nil, err
	}

	return p.Complete(ctx, input.Messages, &input.CompleteOptions)
}
