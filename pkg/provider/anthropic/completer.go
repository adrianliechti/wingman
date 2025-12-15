package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"

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

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		req, err := c.convertMessageRequest(messages, options)

		if err != nil {
			yield(nil, err)
			return
		}

		message := anthropic.Message{}
		stream := c.messages.NewStreaming(ctx, *req)

		for stream.Next() {
			event := stream.Current()

			// HACK: handle empty tool use blocks
			switch event.AsAny().(type) {
			case anthropic.ContentBlockStopEvent:
				block := &message.Content[len(message.Content)-1]

				if block.Type == "tool_use" && len(block.Input) == 0 {
					block.Input = json.RawMessage([]byte("{}"))

					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									Arguments: "{}",
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}
				}
			}

			if err := message.Accumulate(event); err != nil {
				yield(nil, err)
				return
			}

			switch event := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				break

			case anthropic.ContentBlockStartEvent:
				switch event := event.ContentBlock.AsAny().(type) {
				case anthropic.TextBlock:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.TextContent(event.Text),
							},
						},

						Usage: toUsage(message.Usage),
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.ToolUseBlock:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

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

					if !yield(delta, nil) {
						return
					}
				}

			case anthropic.ContentBlockDeltaEvent:
				switch event := event.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.TextContent(event.Text),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.InputJSONDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

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

					if !yield(delta, nil) {
						return
					}
				}

			case anthropic.ContentBlockStopEvent:
				break

			case anthropic.MessageStopEvent:
				delta := &provider.Completion{
					ID:    message.ID,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},

					Usage: toUsage(message.Usage),
				}

				if !yield(delta, nil) {
					return
				}
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
	}
}

func (c *Completer) convertMessageRequest(input []provider.Message, options *provider.CompleteOptions) (*anthropic.MessageNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(8192),
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
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
						blocks = append(blocks, anthropic.NewImageBlockBase64(mime, content))

					case "application/pdf":
						block := anthropic.DocumentBlockParam{
							Source: anthropic.DocumentBlockParamSourceUnion{
								OfBase64: &anthropic.Base64PDFSourceParam{
									Data: content,
								},
							},
						}

						blocks = append(blocks, anthropic.ContentBlockParamUnion{OfDocument: &block})

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
						OfToolUse: &anthropic.ToolUseBlockParam{
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

		var schema anthropic.ToolInputSchemaParam

		schemaData, _ := json.Marshal(t.Parameters)

		if err := json.Unmarshal(schemaData, &schema); err != nil {
			return nil, errors.New("invalid tool parameters schema")
		}

		tool := anthropic.ToolParam{
			Name: t.Name,

			InputSchema: schema,
		}

		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}

		tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
	}

	if options.Schema != nil {
		var schema anthropic.ToolInputSchemaParam

		schemaData, _ := json.Marshal(options.Schema.Schema)

		if err := json.Unmarshal(schemaData, &schema); err != nil {
			return nil, errors.New("invalid tool parameters schema")
		}

		tool := anthropic.ToolParam{
			Name: options.Schema.Name,

			InputSchema: schema,
		}

		if options.Schema.Description != "" {
			tool.Description = anthropic.String(options.Schema.Description)
		}

		req.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{
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

func toUsage(usage anthropic.Usage) *provider.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
	}
}
