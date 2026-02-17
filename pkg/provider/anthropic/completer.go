package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"strings"

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
				case anthropic.ThinkingBlock:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ReasoningContent(provider.Reasoning{
									Text:      event.Thinking,
									Signature: event.Signature,
								}),
							},
						},

						Usage: toUsage(message.Usage),
					}

					if !yield(delta, nil) {
						return
					}

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
				case anthropic.ThinkingDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ReasoningContent(provider.Reasoning{
									Text: event.Thinking,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

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
		Model: anthropic.Model(c.model),

		MaxTokens: 64000,
	}

	isOpus46 := strings.Contains(c.model, "opus-4-6")
	isSonnet46 := strings.Contains(c.model, "sonnet-4-6")

	if isOpus46 || isSonnet46 {
		req.MaxTokens = 128000

		req.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: anthropic.Ptr(anthropic.NewThinkingConfigAdaptiveParam()),
		}

		switch options.Effort {
		case provider.EffortMinimal, provider.EffortLow:
			req.OutputConfig.Effort = anthropic.OutputConfigEffortLow

		case provider.EffortMedium:
			req.OutputConfig.Effort = anthropic.OutputConfigEffortMedium

		case provider.EffortHigh:
			req.OutputConfig.Effort = anthropic.OutputConfigEffortHigh
		}
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

	// First pass: collect system messages
	for _, m := range input {
		if m.Role == provider.MessageRoleSystem {
			for _, c := range m.Content {
				if c.Text != "" {
					system = append(system, anthropic.TextBlockParam{Text: c.Text})
				}
			}
		}
	}

	// Add cache control to the last system block
	if len(system) > 0 {
		system[len(system)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			continue // Already processed above

		case provider.MessageRoleUser:
			var blocks []anthropic.ContentBlockParamUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(text))
				}

				if c.File != nil {
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
						blocks = append(blocks, anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
							Data:      content,
							MediaType: anthropic.Base64ImageSourceMediaType(mime),
						}))

					case "application/pdf":
						blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{
							Data: content,
						}))

					default:
						return nil, errors.New("unsupported content type")
					}
				}

				if c.ToolResult != nil {
					blocks = append(blocks, anthropic.ContentBlockParamUnion{
						OfToolResult: &anthropic.ToolResultBlockParam{
							ToolUseID: c.ToolResult.ID,

							Content: []anthropic.ToolResultBlockParamContentUnion{
								{
									OfText: &anthropic.TextBlockParam{
										Text: c.ToolResult.Data,
									},
								},
							},
						},
					})
				}
			}

			message := anthropic.NewUserMessage(blocks...)
			messages = append(messages, message)

			// Mark user messages for potential cache control (will be set on the last one)
			// The cache point will be added after all messages are processed

		case provider.MessageRoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(text))
				}

				if c.Reasoning != nil {
					// Include thinking blocks for conversation continuity
					blocks = append(blocks, anthropic.NewThinkingBlock(c.Reasoning.Signature, c.Reasoning.Text))
				}

				if c.ToolCall != nil {
					var input map[string]any

					if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &input); err != nil || input == nil {
						input = map[string]any{}
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

			message := anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: blocks,
			}

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

	// Add cache control to the last tool
	if len(tools) > 0 {
		tools[len(tools)-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	if options.Schema != nil {
		req.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: options.Schema.Schema}
	}

	if len(system) > 0 {
		req.System = system
	}

	if len(tools) > 0 {
		req.Tools = tools
	}

	if len(messages) > 0 {
		// Add cache control to the last content block of the last user message
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == anthropic.MessageParamRoleUser {
				if len(messages[i].Content) > 0 {
					lastBlock := &messages[i].Content[len(messages[i].Content)-1]
					setCacheControl(lastBlock)
				}
				break
			}
		}

		req.Messages = messages
	}

	return req, nil
}

// setCacheControl sets the cache control on a content block
func setCacheControl(block *anthropic.ContentBlockParamUnion) {
	cacheControl := anthropic.NewCacheControlEphemeralParam()

	switch {
	case block.OfText != nil:
		block.OfText.CacheControl = cacheControl
	case block.OfImage != nil:
		block.OfImage.CacheControl = cacheControl
	case block.OfDocument != nil:
		block.OfDocument.CacheControl = cacheControl
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = cacheControl
	}
}

func toUsage(usage anthropic.Usage) *provider.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),

		CacheReadInputTokens:     int(usage.CacheReadInputTokens),
		CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
	}
}
