package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/to"

	"github.com/anthropics/anthropic-sdk-go"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
	messages *anthropic.MessageService
}

func NewCompleter(url, model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Completer{
		Config:   cfg,
		messages: anthropic.NewMessageService(cfg.Options()...),
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := c.convertMessageRequest(messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		return c.completeStream(ctx, *req, options)
	}

	return c.complete(ctx, *req, options)
}

func (c *Completer) complete(ctx context.Context, req anthropic.MessageNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	message, err := c.messages.New(ctx, req)

	if err != nil {
		return nil, convertError(err)
	}

	return &provider.Completion{
		ID:     message.ID,
		Reason: toCompletionResult(message.StopReason),

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: toContent(message.Content),

			ToolCalls: toToolCalls(message.Content),
		},

		Usage: &provider.Usage{
			InputTokens:  int(message.Usage.InputTokens),
			OutputTokens: int(message.Usage.OutputTokens),
		},
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req anthropic.MessageNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := c.messages.NewStreaming(ctx, req)

	message := anthropic.Message{}

	for stream.Next() {
		event := stream.Current()

		if err := message.Accumulate(event); err != nil {
			return nil, err
		}

		switch event := event.AsUnion().(type) {
		case anthropic.MessageStartEvent:
			break

		case anthropic.ContentBlockStartEvent:
			delta := provider.Completion{
				ID:      message.ID,
				Message: to.Ptr(provider.AssistantMessage(event.ContentBlock.Text)),
			}

			if event.ContentBlock.Name != "" {
				delta.Message.ToolCalls = []provider.ToolCall{
					{
						ID:   event.ContentBlock.ID,
						Name: event.ContentBlock.Name,
					},
				}

				if options.Schema != nil {
					delta.Message.ToolCalls = nil
				}
			}

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}

		case anthropic.ContentBlockDeltaEvent:
			delta := provider.Completion{
				ID: message.ID,

				Message: to.Ptr(provider.AssistantMessage(event.Delta.Text)),
			}

			if event.Delta.PartialJSON != "" {
				delta.Message.ToolCalls = []provider.ToolCall{
					{
						Arguments: event.Delta.PartialJSON,
					},
				}

				if options.Schema != nil {
					delta.Message.ToolCalls = nil

					delta.Message.Content = provider.MessageContent{
						{
							Text1: event.Delta.PartialJSON,
						},
					}
				}
			}

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}

		case anthropic.ContentBlockStopEvent:
			break

		case anthropic.MessageStopEvent:
			delta := provider.Completion{
				ID:     message.ID,
				Reason: toCompletionResult(message.StopReason),

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},

				Usage: &provider.Usage{
					InputTokens:  int(message.Usage.InputTokens),
					OutputTokens: int(message.Usage.OutputTokens),
				},
			}

			if options.Schema != nil && delta.Reason == provider.CompletionReasonTool {
				delta.Reason = provider.CompletionReasonStop
			}

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return &provider.Completion{
		ID:     message.ID,
		Reason: toCompletionResult(message.StopReason),

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: toContent(message.Content),

			ToolCalls: toToolCalls(message.Content),
		},

		Usage: &provider.Usage{
			InputTokens:  int(message.Usage.InputTokens),
			OutputTokens: int(message.Usage.OutputTokens),
		},
	}, nil
}

func (c *Completer) convertMessageRequest(input []provider.Message, options *provider.CompleteOptions) (*anthropic.MessageNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &anthropic.MessageNewParams{
		Model:     anthropic.F(c.model),
		MaxTokens: anthropic.F(int64(4096)),
	}

	var system []anthropic.TextBlockParam

	var tools []anthropic.ToolUnionUnionParam
	var messages []anthropic.MessageParam

	if options.Stop != nil {
		req.StopSequences = anthropic.F(options.Stop)
	}

	if options.MaxTokens != nil {
		req.MaxTokens = anthropic.F(int64(*options.MaxTokens))
	}

	if options.Temperature != nil {
		req.Temperature = anthropic.F(float64(*options.Temperature))
	}

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			system = append(system, anthropic.NewTextBlock(m.Content.String()))

		case provider.MessageRoleUser:
			blocks := []anthropic.ContentBlockParamUnion{}

			for _, c := range m.Content {
				if c.Text1 != "" {
					blocks = append(blocks, anthropic.NewTextBlock(c.Text1))
				}
			}

			for _, f := range m.Files {
				data, err := io.ReadAll(f.Content)

				if err != nil {
					return nil, err
				}

				mime := f.ContentType
				content := base64.StdEncoding.EncodeToString(data)

				switch mime {
				case "image/jpeg", "image/png", "image/gif", "image/webp":
					blocks = append(blocks, anthropic.NewImageBlockBase64(mime, content))

				case "application/pdf":
					block := anthropic.DocumentBlockParam{
						Type: anthropic.F(anthropic.DocumentBlockParamTypeDocument),

						Source: anthropic.F(anthropic.DocumentBlockParamSourceUnion(anthropic.Base64PDFSourceParam{
							Type:      anthropic.F(anthropic.Base64PDFSourceTypeBase64),
							Data:      anthropic.F(content),
							MediaType: anthropic.F(anthropic.Base64PDFSourceMediaTypeApplicationPDF),
						})),
					}

					blocks = append(blocks, block)

				default:
					return nil, errors.New("unsupported content type")
				}
			}

			message := anthropic.NewUserMessage(blocks...)
			messages = append(messages, message)

		case provider.MessageRoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}

			for _, c := range m.Content {
				if c.Text1 != "" {
					blocks = append(blocks, anthropic.NewTextBlock(c.Text1))
				}
			}

			for _, t := range m.ToolCalls {
				var input any

				if err := json.Unmarshal([]byte(t.Arguments), &input); err != nil {
					input = t.Arguments
				}

				blocks = append(blocks, anthropic.NewToolUseBlockParam(t.ID, t.Name, input))
			}

			message := anthropic.NewAssistantMessage(blocks...)
			messages = append(messages, message)

		case provider.MessageRoleTool:
			message := anthropic.NewUserMessage(anthropic.NewToolResultBlock(m.Tool, m.Content.String(), false))
			messages = append(messages, message)
		}
	}

	for _, t := range options.Tools {
		if t.Name == "" {
			continue
		}

		tool := anthropic.ToolParam{
			Name:        anthropic.F(t.Name),
			InputSchema: anthropic.F[interface{}](t.Parameters),
		}

		if t.Description != "" {
			tool.Description = anthropic.F(t.Description)
		}

		tools = append(tools, tool)
	}

	if options.Schema != nil {
		tool := anthropic.ToolParam{
			Name:        anthropic.F(options.Schema.Name),
			InputSchema: anthropic.F(any(options.Schema.Schema)),
		}

		if options.Schema.Description != "" {
			tool.Description = anthropic.F(options.Schema.Description)
		}

		req.ToolChoice = anthropic.F[anthropic.ToolChoiceUnionParam](anthropic.ToolChoiceToolParam{
			Type: anthropic.F(anthropic.ToolChoiceToolTypeTool),
			Name: anthropic.F(options.Schema.Name),
		})

		tools = append(tools, tool)
	}

	if len(system) > 0 {
		req.System = anthropic.F(system)
	}

	if len(tools) > 0 {
		req.Tools = anthropic.F(tools)
	}

	if len(messages) > 0 {
		req.Messages = anthropic.F(messages)
	}

	return req, nil
}

func toContent(blocks []anthropic.ContentBlock) []provider.Content {
	var parts []provider.Content

	for _, b := range blocks {
		if b.Type == anthropic.ContentBlockTypeText {
			parts = append(parts, provider.Content{
				Text1: b.Text,
			})
		}
	}

	return parts
}

func toToolCalls(blocks []anthropic.ContentBlock) []provider.ToolCall {
	var result []provider.ToolCall

	for _, b := range blocks {
		if b.Type != anthropic.ContentBlockTypeToolUse {
			continue
		}

		input, _ := b.Input.MarshalJSON()

		call := provider.ToolCall{
			ID: b.ID,

			Name:      b.Name,
			Arguments: string(input),
		}

		result = append(result, call)
	}

	return result
}

func toCompletionResult(val anthropic.MessageStopReason) provider.CompletionReason {
	switch val {
	case anthropic.MessageStopReasonEndTurn:
		return provider.CompletionReasonStop

	case anthropic.MessageStopReasonMaxTokens:
		return provider.CompletionReasonLength

	case anthropic.MessageStopReasonStopSequence:
		return provider.CompletionReasonStop

	case anthropic.MessageStopReasonToolUse:
		return provider.CompletionReasonTool

	default:
		return ""
	}
}
