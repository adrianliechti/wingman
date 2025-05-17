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
type CompletionReasoningEffort = provider.ReasoningEffort

type CompleteOptions = provider.CompleteOptions
type CompleteStreamHandler = provider.StreamHandler

type Tool = provider.Tool
type ToolCall = provider.ToolCall
type ToolResult = provider.ToolResult

type Schema = provider.Schema

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
