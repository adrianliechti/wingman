package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
	messages anthropic.MessageService
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
		},

		Usage: toUsage(message.Usage),
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req anthropic.MessageNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	result := provider.CompletionAccumulator{}

	message := anthropic.Message{}
	stream := c.messages.NewStreaming(ctx, req)

	for stream.Next() {
		event := stream.Current()

		if err := message.Accumulate(event); err != nil {
			return nil, err
		}

		switch event := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			break

		case anthropic.ContentBlockStartEvent:
			switch event := event.ContentBlock.AsAny().(type) {
			case anthropic.TextBlock:
				delta := provider.Completion{
					ID:     message.ID,
					Reason: toCompletionResult(message.StopReason),

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(event.Text),
						},
					},

					Usage: toUsage(message.Usage),
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}

			case anthropic.ToolUseBlock:
				delta := provider.Completion{
					ID:     message.ID,
					Reason: toCompletionResult(message.StopReason),

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								ID:   event.ID,
								Name: event.Name,
							}),
						},
					},

					Usage: toUsage(message.Usage),
				}

				if options.Schema != nil {
					delta.Message.Content = []provider.Content{
						provider.TextContent(""),
					}
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}
			}

		case anthropic.ContentBlockDeltaEvent:
			switch event := event.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				delta := provider.Completion{
					ID: message.ID,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(event.Text),
						},
					},
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}

			case anthropic.InputJSONDelta:
				delta := provider.Completion{
					ID: message.ID,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								Arguments: event.PartialJSON,
							}),
						},
					},
				}

				if options.Schema != nil {
					delta.Message.Content = []provider.Content{
						{
							Text: event.PartialJSON,
						},
					}
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}
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

				Usage: toUsage(message.Usage),
			}

			if options.Schema != nil && delta.Reason == provider.CompletionReasonTool {
				delta.Reason = provider.CompletionReasonStop
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return result.Result(), nil
}

func (c *Completer) convertMessageRequest(input []provider.Message, options *provider.CompleteOptions) (*anthropic.MessageNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: int64(4096),
	}

	var system []anthropic.TextBlockParam

	var tools []anthropic.ToolUnionParam
	var messages []anthropic.MessageParam

	if options.Stop != nil {
		req.StopSequences = options.Stop
	}

	if options.MaxTokens != nil {
		req.MaxTokens = int64(*options.MaxTokens)
	}

	if options.Temperature != nil {
		req.Temperature = anthropic.Float(float64(*options.Temperature))
	}

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			for _, c := range m.Content {
				if c.Text != "" {
					system = append(system, anthropic.TextBlockParam{Text: c.Text})
				}
			}

		case provider.MessageRoleUser:
			var blocks []anthropic.ContentBlockParamUnion

			for _, c := range m.Content {
				if c.Text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(c.Text))
				}

				if c.File != nil {
					data, err := io.ReadAll(c.File.Content)

					if err != nil {
						return nil, err
					}

					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(data)

					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
						blocks = append(blocks, anthropic.NewImageBlockBase64(mime, content))

					case "application/pdf":
						block := anthropic.DocumentBlockParam{
							Source: anthropic.DocumentBlockParamSourceUnion{
								OfBase64PDFSource: &anthropic.Base64PDFSourceParam{
									Data: content,
								},
							},
						}

						blocks = append(blocks, anthropic.ContentBlockParamUnion{OfRequestDocumentBlock: &block})

					default:
						return nil, errors.New("unsupported content type")
					}
				}

				if c.ToolResult != nil {
					blocks = append(blocks, anthropic.NewToolResultBlock(c.ToolResult.ID, c.ToolResult.Data, false))
				}
			}

			message := anthropic.NewUserMessage(blocks...)
			messages = append(messages, message)

		case provider.MessageRoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion

			for _, c := range m.Content {
				if c.Text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(c.Text))
				}

				if c.ToolCall != nil {
					var input any

					if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &input); err != nil {
						input = c.ToolCall.Arguments
					}

					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfRequestToolUseBlock: &anthropic.ToolUseBlockParam{
							ID:    c.ToolCall.ID,
							Name:  c.ToolCall.Name,
							Input: input,
						},
					})
				}
			}

			message := anthropic.NewAssistantMessage(blocks...)
			messages = append(messages, message)
		}
	}

	for _, t := range options.Tools {
		if t.Name == "" {
			continue
		}

		tool := anthropic.ToolParam{
			Name: t.Name,

			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: t.Parameters["properties"],
			},
		}

		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}

		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
	}

	if options.Schema != nil {
		tool := anthropic.ToolParam{
			Name: options.Schema.Name,

			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: options.Schema.Schema,
			},
		}

		if options.Schema.Description != "" {
			tool.Description = anthropic.String(options.Schema.Description)
		}

		req.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfToolChoiceTool: &anthropic.ToolChoiceToolParam{
				Name: options.Schema.Name,
			},
		}

		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
	}

	if len(system) > 0 {
		req.System = system
	}

	if len(tools) > 0 {
		req.Tools = tools
	}

	if len(messages) > 0 {
		req.Messages = messages
	}

	return req, nil
}

func toContent(blocks []anthropic.ContentBlockUnion) []provider.Content {
	var parts []provider.Content

	for _, b := range blocks {
		switch b := b.AsAny().(type) {
		case anthropic.TextBlock:
			parts = append(parts, provider.TextContent(b.Text))

		case anthropic.ToolUseBlock:
			input, _ := json.Marshal(b.Input)

			call := provider.ToolCall{
				ID: b.ID,

				Name:      b.Name,
				Arguments: string(input),
			}

			parts = append(parts, provider.ToolCallContent(call))
		}
	}

	return parts
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

func toUsage(usage anthropic.Usage) *provider.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
	}
}
