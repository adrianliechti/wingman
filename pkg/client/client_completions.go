package client

import (
	"context"
	"iter"
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
type CompletionAccumulator = provider.CompletionAccumulator

type CompleteOptions = provider.CompleteOptions

type Content = provider.Content

type Tool = provider.Tool
type ToolCall = provider.ToolCall
type ToolResult = provider.ToolResult

type Schema = provider.Schema
type Effort = provider.Effort
type Verbosity = provider.Verbosity

func SystemMessage(content string) Message {
	return provider.SystemMessage(content)
}

func UserMessage(content string) Message {
	return provider.UserMessage(content)
}

func AssistantMessage(content string) Message {
	return provider.AssistantMessage(content)
}

func ToolMessage(id, content string) Message {
	return provider.ToolMessage(id, content)
}

type CompletionRequest struct {
	CompleteOptions

	Model    string
	Messages []Message
}

func (r *CompletionService) New(ctx context.Context, input CompletionRequest, opts ...RequestOption) (*Completion, error) {
	acc := CompletionAccumulator{}

	for c, err := range r.NewStream(ctx, input, opts...) {
		if err != nil {
			return nil, err
		}

		acc.Add(*c)
	}

	return acc.Result(), nil
}

func (r *CompletionService) NewStream(ctx context.Context, input CompletionRequest, opts ...RequestOption) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
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
			yield(nil, err)
			return
		}

		for completion, err := range p.Complete(ctx, input.Messages, &input.CompleteOptions) {
			if !yield(completion, err) {
				return
			}
		}
	}
}
