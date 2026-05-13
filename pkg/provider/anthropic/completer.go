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
	messages anthropic.BetaMessageService
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
		messages: anthropic.NewBetaMessageService(cfg.Options()...),
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

		message := anthropic.BetaMessage{}
		stream := c.messages.NewStreaming(ctx, *req)

		for stream.Next() {
			event := stream.Current()

			// HACK: handle empty tool use blocks
			switch event.AsAny().(type) {
			case anthropic.BetaRawContentBlockStopEvent:
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
			case anthropic.BetaRawMessageStartEvent:
				break

			case anthropic.BetaRawContentBlockStartEvent:
				switch event := event.ContentBlock.AsAny().(type) {
				case anthropic.BetaThinkingBlock:
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

				case anthropic.BetaTextBlock:
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

				case anthropic.BetaRedactedThinkingBlock:
					// Redacted thinking blocks are silently skipped

				case anthropic.BetaCompactionBlock:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.CompactionContent(provider.Compaction{
									Signature: event.Content,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaToolUseBlock:
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

			case anthropic.BetaRawContentBlockDeltaEvent:
				switch event := event.Delta.AsAny().(type) {
				case anthropic.BetaThinkingDelta:
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

				case anthropic.BetaSignatureDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ReasoningContent(provider.Reasoning{
									Signature: event.Signature,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaCompactionContentBlockDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.CompactionContent(provider.Compaction{
									Signature: event.Content,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaTextDelta:
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

				case anthropic.BetaInputJSONDelta:
					currentBlock := message.Content[len(message.Content)-1]

					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        currentBlock.ID,
									Arguments: event.PartialJSON,
								}),
							},
						},
					}

					if options.Schema != nil {
						delta.Message.Content = []provider.Content{
							provider.TextContent(event.PartialJSON),
						}
					}

					if !yield(delta, nil) {
						return
					}
				}

			case anthropic.BetaRawContentBlockStopEvent:
				break

			case anthropic.BetaRawMessageStopEvent:
				delta := &provider.Completion{
					ID:    message.ID,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},

					Usage: toUsage(message.Usage),
				}

				switch message.StopReason {
				case anthropic.BetaStopReasonMaxTokens:
					delta.Status = provider.CompletionStatusIncomplete
				case anthropic.BetaStopReasonRefusal:
					delta.Status = provider.CompletionStatusRefused
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

func (c *Completer) convertMessageRequest(input []provider.Message, options *provider.CompleteOptions) (*anthropic.BetaMessageNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &anthropic.BetaMessageNewParams{
		Model: anthropic.Model(c.model),

		MaxTokens:    64000,
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}

	if isAdaptiveThinkingModel(c.model) {
		req.MaxTokens = 128000

		if options.ReasoningOptions != nil {
			display := anthropic.BetaThinkingConfigAdaptiveDisplaySummarized

			if !options.ReasoningOptions.IncludeSummary {
				display = anthropic.BetaThinkingConfigAdaptiveDisplayOmitted
			}

			req.Thinking = anthropic.BetaThinkingConfigParamUnion{
				OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
					Display: display,
				},
			}

			switch options.ReasoningOptions.Effort {
			case provider.EffortNone, provider.EffortMinimal, provider.EffortLow:
				req.OutputConfig.Effort = anthropic.BetaOutputConfigEffortLow

			case provider.EffortMedium:
				req.OutputConfig.Effort = anthropic.BetaOutputConfigEffortMedium

			case provider.EffortHigh:
				req.OutputConfig.Effort = anthropic.BetaOutputConfigEffortHigh

			case provider.EffortXHigh:
				req.OutputConfig.Effort = anthropic.BetaOutputConfigEffortXhigh

			case provider.EffortMax:
				req.OutputConfig.Effort = anthropic.BetaOutputConfigEffortMax
			}
		}
	}

	var system []anthropic.BetaTextBlockParam

	var tools []anthropic.BetaToolUnionParam
	var messages []anthropic.BetaMessageParam

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
					system = append(system, anthropic.BetaTextBlockParam{Text: c.Text})
				}
			}

		case provider.MessageRoleUser:
			var blocks []anthropic.BetaContentBlockParamUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					blocks = append(blocks, anthropic.NewBetaTextBlock(text))
				}

				if c.File != nil {
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
						blocks = append(blocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
							Data:      content,
							MediaType: anthropic.BetaBase64ImageSourceMediaType(mime),
						}))

					case "application/pdf":
						blocks = append(blocks, anthropic.NewBetaDocumentBlock(anthropic.BetaBase64PDFSourceParam{
							Data: content,
						}))

					default:
						return nil, errors.New("unsupported content type")
					}
				}

				if c.ToolResult != nil {
					var parts []anthropic.BetaToolResultBlockParamContentUnion

					for _, p := range c.ToolResult.Parts {
						if p.Text != "" {
							parts = append(parts, anthropic.BetaToolResultBlockParamContentUnion{
								OfText: &anthropic.BetaTextBlockParam{Text: p.Text},
							})
						}

						if p.File != nil {
							mime := p.File.ContentType
							content := base64.StdEncoding.EncodeToString(p.File.Content)

							switch mime {
							case "image/jpeg", "image/png", "image/gif", "image/webp":
								parts = append(parts, anthropic.BetaToolResultBlockParamContentUnion{
									OfImage: &anthropic.BetaImageBlockParam{
										Source: anthropic.BetaImageBlockParamSourceUnion{
											OfBase64: &anthropic.BetaBase64ImageSourceParam{
												Data:      content,
												MediaType: anthropic.BetaBase64ImageSourceMediaType(mime),
											},
										},
									},
								})

							case "application/pdf":
								parts = append(parts, anthropic.BetaToolResultBlockParamContentUnion{
									OfDocument: &anthropic.BetaRequestDocumentBlockParam{
										Source: anthropic.BetaRequestDocumentBlockSourceUnionParam{
											OfBase64: &anthropic.BetaBase64PDFSourceParam{
												Data: content,
											},
										},
									},
								})

							default:
								return nil, errors.New("unsupported tool result content type")
							}
						}
					}

					if len(parts) == 0 {
						parts = []anthropic.BetaToolResultBlockParamContentUnion{
							{OfText: &anthropic.BetaTextBlockParam{Text: ""}},
						}
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfToolResult: &anthropic.BetaToolResultBlockParam{
							ToolUseID: c.ToolResult.ID,
							Content:   parts,
						},
					})
				}
			}

			message := anthropic.NewBetaUserMessage(blocks...)
			messages = append(messages, message)

		case provider.MessageRoleAssistant:
			var blocks []anthropic.BetaContentBlockParamUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					blocks = append(blocks, anthropic.NewBetaTextBlock(text))
				}

				if c.Reasoning != nil && c.Reasoning.Signature != "" {
					// Include thinking blocks for conversation continuity
					blocks = append(blocks, anthropic.NewBetaThinkingBlock(c.Reasoning.Signature, c.Reasoning.Text))
				}

				if c.ToolCall != nil {
					var input map[string]any

					if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &input); err != nil || input == nil {
						input = map[string]any{}
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfToolUse: &anthropic.BetaToolUseBlockParam{
							ID:    c.ToolCall.ID,
							Name:  c.ToolCall.Name,
							Input: input,
						},
					})
				}
			}

			message := anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleAssistant,
				Content: blocks,
			}

			messages = append(messages, message)
		}
	}

	for _, t := range options.Tools {
		if t.Name == "" {
			continue
		}

		var schema anthropic.BetaToolInputSchemaParam

		schemaData, _ := json.Marshal(t.Parameters)

		if err := json.Unmarshal(schemaData, &schema); err != nil {
			return nil, errors.New("invalid tool parameters schema")
		}

		tool := anthropic.BetaToolParam{
			Name: t.Name,

			InputSchema: schema,
		}

		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}

		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}

	if options.Schema != nil {
		schema := options.Schema.Schema
		if schema == nil {
			// json_object mode: Anthropic requires a non-empty schema, and rejects
			// `type: object` unless additionalProperties is explicitly false.
			schema = map[string]any{"type": "object", "additionalProperties": false}
		}
		req.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: schema}
	}

	if options.CompactionOptions != nil && options.CompactionOptions.Threshold > 0 {
		req.Betas = append(req.Betas, "compact-2026-01-12")

		req.ContextManagement = anthropic.BetaContextManagementConfigParam{
			Edits: []anthropic.BetaContextManagementConfigEditUnionParam{
				{
					OfCompact20260112: &anthropic.BetaCompact20260112EditParam{
						Trigger: anthropic.BetaInputTokensTriggerParam{
							Value: int64(options.CompactionOptions.Threshold),
						},
					},
				},
			},
		}
	}

	if len(system) > 0 {
		req.System = system
	}

	if options.TextEditorTool != nil {
		req.Tools = append(req.Tools, anthropic.BetaToolUnionParam{
			OfTextEditor20250728: &anthropic.BetaToolTextEditor20250728Param{},
		})
	}

	if options.ComputerUseTool != nil {
		req.Betas = append(req.Betas, "computer-use-2025-11-24")

		w, h := int64(options.ComputerUseTool.DisplayWidth), int64(options.ComputerUseTool.DisplayHeight)
		if w == 0 {
			w = 1024
		}
		if h == 0 {
			h = 768
		}

		req.Tools = append(req.Tools, anthropic.BetaToolUnionParam{
			OfComputerUseTool20251124: &anthropic.BetaToolComputerUse20251124Param{
				DisplayWidthPx:  w,
				DisplayHeightPx: h,
			},
		})
	}

	if len(tools) > 0 {
		req.Tools = append(req.Tools, tools...)
	}

	if options.ToolOptions != nil {
		forcesTool := false

		switch options.ToolOptions.Choice {
		case provider.ToolChoiceNone:
			req.ToolChoice = anthropic.BetaToolChoiceUnionParam{
				OfNone: anthropic.Ptr(anthropic.NewBetaToolChoiceNoneParam()),
			}

		case provider.ToolChoiceAuto:
			p := &anthropic.BetaToolChoiceAutoParam{}

			if options.ToolOptions.DisableParallelToolCalls {
				p.DisableParallelToolUse = anthropic.Bool(true)
			}

			req.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfAuto: p}

		case provider.ToolChoiceAny:
			forcesTool = true

			if len(options.ToolOptions.Allowed) == 1 {
				req.ToolChoice = anthropic.BetaToolChoiceUnionParam{
					OfTool: &anthropic.BetaToolChoiceToolParam{
						Name: options.ToolOptions.Allowed[0],
					},
				}
			} else {
				p := &anthropic.BetaToolChoiceAnyParam{}

				if options.ToolOptions.DisableParallelToolCalls {
					p.DisableParallelToolUse = anthropic.Bool(true)
				}

				req.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfAny: p}
			}
		}

		// Claude doesn't allow thinking with forced tool_choice
		if forcesTool {
			req.Thinking = anthropic.BetaThinkingConfigParamUnion{}
		}
	}

	if len(messages) > 0 {
		req.Messages = messages
	}

	return req, nil
}

func toUsage(usage anthropic.BetaUsage) *provider.Usage {
	if usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.CacheReadInputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),

		CacheReadInputTokens:     int(usage.CacheReadInputTokens),
		CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
	}
}
