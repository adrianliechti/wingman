package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/adrianliechti/wingman/pkg/provider/tools/toolsearch"

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

		toolAliases := provider.ToolAliases(options.Tools)

		message := anthropic.BetaMessage{}
		stream := c.messages.NewStreaming(ctx, *req)

		toolArgsSeen := map[int64]bool{}

		for stream.Next() {
			event := stream.Current()

			// HACK: tool use blocks without input_json_delta would otherwise
			// accumulate empty arguments downstream — normalize to "{}"
			switch event := event.AsAny().(type) {
			case anthropic.BetaRawContentBlockStopEvent:
				if int(event.Index) >= len(message.Content) {
					break
				}

				block := &message.Content[event.Index]

				if block.Type == "tool_use" && !toolArgsSeen[event.Index] {
					if len(block.Input) == 0 {
						block.Input = json.RawMessage([]byte("{}"))
					}

					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        block.ID,
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

			// SDK's Accumulate copies OutputTokens from message_delta but drops
			// OutputTokensDetails.ThinkingTokens. Patch it in manually.
			if delta, ok := event.AsAny().(anthropic.BetaRawMessageDeltaEvent); ok {
				message.Usage.OutputTokensDetails.ThinkingTokens = delta.Usage.OutputTokensDetails.ThinkingTokens
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
					// Round-trip the opaque blob so multi-turn tool use survives redaction
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ReasoningContent(provider.Reasoning{
									Signature: event.Data,
									Redacted:  true,
								}),
							},
						},

						Usage: toUsage(message.Usage),
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaCompactionBlock:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.CompactionContent(provider.Compaction{
									Content:   event.Content,
									Signature: event.EncryptedContent,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaToolUseBlock:
					call := provider.UnflattenToolCall(toolAliases, provider.ToolCall{
						ID:   event.ID,
						Name: event.Name,
					})

					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(call),
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
				blockIndex := event.Index

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
									Content:   event.Content,
									Signature: event.EncryptedContent,
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
					if int(blockIndex) >= len(message.Content) {
						break
					}

					if event.PartialJSON != "" {
						toolArgsSeen[blockIndex] = true
					}

					currentBlock := message.Content[blockIndex]

					// server-executed tools (tool search, web search) resolve
					// within the turn — their input must not leak as tool calls
					if currentBlock.Type != "tool_use" {
						break
					}

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
				case anthropic.BetaStopReasonStopSequence:
					delta.StopSequence = message.StopSequence
				case anthropic.BetaStopReasonMaxTokens, anthropic.BetaStopReasonModelContextWindowExceeded:
					delta.Status = provider.CompletionStatusIncomplete
				case anthropic.BetaStopReasonRefusal:
					delta.Status = provider.CompletionStatusRefused

					if message.StopDetails.JSON.Type.Valid() {
						delta.StopDetails = &provider.StopDetails{
							Type:        string(message.StopDetails.Type),
							Category:    string(message.StopDetails.Category),
							Explanation: message.StopDetails.Explanation,
						}
					}
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

	if !isLegacyModel(c.model) {
		req.MaxTokens = 128000

		if reasoning := options.ReasoningOptions; reasoning != nil {
			if reasoning.Type == provider.ReasoningTypeAdaptive {
				display := anthropic.BetaThinkingConfigAdaptiveDisplaySummarized
				if !reasoning.IncludeSummary {
					display = anthropic.BetaThinkingConfigAdaptiveDisplayOmitted
				}

				req.Thinking = anthropic.BetaThinkingConfigParamUnion{
					OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{Display: display},
				}
			}

			if effort := outputEffort(reasoning.Effort); effort != "" {
				req.OutputConfig.Effort = effort
			}
		}
	}

	var system []anthropic.BetaTextBlockParam

	var tools []anthropic.BetaToolUnionParam
	var messages []anthropic.BetaMessageParam

	var hasCompaction bool

	if options.Stop != nil {
		req.StopSequences = options.Stop
	}

	if options.MaxTokens != nil {
		req.MaxTokens = int64(*options.MaxTokens)
	}

	var hasToolSearch bool

	for _, t := range options.Tools {
		if t.Kind == provider.ToolKindToolSearch && t.Execution != "client" {
			hasToolSearch = true
		}
	}

	// Tools a client-executed tool_search returned in prior turns — they must
	// be available (non-deferred) so the model can call them.
	var discovered []provider.Tool

	// Tools already called in the conversation stay loaded as well.
	usedNames := map[string]bool{}

	for _, m := range input {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				usedNames[provider.FlattenToolName(*c.ToolCall)] = true
			}
		}
	}

	for _, m := range input {
		switch m.Role {
		case provider.MessageRoleSystem:
			var texts []anthropic.BetaTextBlockParam

			for _, c := range m.Content {
				if c.Text != "" {
					texts = append(texts, anthropic.BetaTextBlockParam{Text: c.Text})
				}
			}

			if len(texts) == 0 {
				break
			}

			if len(messages) == 0 || isLegacyModel(c.model) {
				system = append(system, texts...)
				break
			}

			blocks := make([]anthropic.BetaContentBlockParamUnion, len(texts))
			for i := range texts {
				blocks[i] = anthropic.BetaContentBlockParamUnion{OfText: &texts[i]}
			}

			messages = append(messages, anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleSystem,
				Content: blocks,
			})

		case provider.MessageRoleUser:
			// tool_result blocks must precede other content in a user message
			var blocks []anthropic.BetaContentBlockParamUnion
			var contentBlocks []anthropic.BetaContentBlockParamUnion

			for _, c := range m.Content {
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					contentBlocks = append(contentBlocks, anthropic.NewBetaTextBlock(text))
				}

				if c.File != nil {
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
						contentBlocks = append(contentBlocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
							Data:      content,
							MediaType: anthropic.BetaBase64ImageSourceMediaType(mime),
						}))

					case "application/pdf":
						contentBlocks = append(contentBlocks, anthropic.NewBetaDocumentBlock(anthropic.BetaBase64PDFSourceParam{
							Data: content,
						}))

					default:
						return nil, fmt.Errorf("unsupported content type: %s", mime)
					}
				}

				if c.ToolResult != nil {
					if c.ToolResult.Kind == provider.ToolKindToolSearch {
						discovered = append(discovered, toolsearch.Tools(c.ToolResult.Payload)...)

						if c.ToolResult.Execution != "client" {
							// server-side search happened inside another
							// backend's turn — only the discovered tools matter
							continue
						}

						blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
							OfToolResult: &anthropic.BetaToolResultBlockParam{
								ToolUseID: c.ToolResult.ID,
								Content: []anthropic.BetaToolResultBlockParamContentUnion{
									{OfText: &anthropic.BetaTextBlockParam{Text: string(c.ToolResult.Payload)}},
								},
							},
						})
						continue
					}

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
								return nil, fmt.Errorf("unsupported content type: %s", mime)
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

			blocks = append(blocks, contentBlocks...)

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
					if c.Reasoning.Redacted {
						blocks = append(blocks, anthropic.NewBetaRedactedThinkingBlock(c.Reasoning.Signature))
					} else {
						blocks = append(blocks, anthropic.NewBetaThinkingBlock(c.Reasoning.Signature, c.Reasoning.Text))
					}
				}

				if c.Compaction != nil && (c.Compaction.Content != "" || c.Compaction.Signature != "") {
					hasCompaction = true

					compaction := &anthropic.BetaCompactionBlockParam{}

					if c.Compaction.Content != "" {
						compaction.Content = anthropic.String(c.Compaction.Content)
					}

					if c.Compaction.Signature != "" {
						compaction.EncryptedContent = anthropic.String(c.Compaction.Signature)
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfCompaction: compaction,
					})
				}

				if c.ToolCall != nil {
					if c.ToolCall.Kind == provider.ToolKindToolSearch && c.ToolCall.Execution != "client" {
						continue
					}

					var input map[string]any

					if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &input); err != nil || input == nil {
						input = map[string]any{}
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfToolUse: &anthropic.BetaToolUseBlockParam{
							ID:    c.ToolCall.ID,
							Name:  provider.FlattenToolName(*c.ToolCall),
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

	toolList := provider.FlattenTools(options.Tools)

	defined := map[string]bool{}
	for _, t := range toolList {
		defined[t.Name] = true
	}

	discoveredNames := map[string]bool{}
	for _, t := range provider.FlattenTools(discovered) {
		discoveredNames[t.Name] = true

		if !defined[t.Name] {
			defined[t.Name] = true
			toolList = append(toolList, t)
		}
	}

	for _, t := range toolList {
		if t.Kind == provider.ToolKindToolSearch {
			if t.Execution == "client" {
				t = toolsearch.FunctionTool(t)
			} else if strings.Contains(t.Name, "bm25") {
				tools = append(tools, anthropic.BetaToolUnionParam{
					OfToolSearchToolBm25_20251119: &anthropic.BetaToolSearchToolBm25_20251119Param{
						Type: "tool_search_tool_bm25_20251119",
					},
				})
				continue
			} else {
				tools = append(tools, anthropic.BetaToolUnionParam{
					OfToolSearchToolRegex20251119: &anthropic.BetaToolSearchToolRegex20251119Param{
						Type: "tool_search_tool_regex_20251119",
					},
				})
				continue
			}
		}

		if t.Kind == provider.ToolKindTextEditor {
			if t.Name == texteditor.NameApplyPatch {
				// apply_patch dialect — emulate as a function tool so calls and
				// results stay in the client's dialect end-to-end
				t = texteditor.FunctionTool(t)
			} else {
				editor := &anthropic.BetaToolTextEditor20250728Param{}

				if t.MaxCharacters > 0 {
					editor.MaxCharacters = anthropic.Int(int64(t.MaxCharacters))
				}

				tools = append(tools, anthropic.BetaToolUnionParam{
					OfTextEditor20250728: editor,
				})
				continue
			}
		}

		if t.Kind == provider.ToolKindComputer {
			if t.Dialect == computeruse.DialectOpenAI {
				// OpenAI dialect — emulate as a function tool so calls and
				// results stay in the client's dialect end-to-end
				t = computeruse.FunctionTool(t)
			} else {
				req.Betas = append(req.Betas, "computer-use-2025-11-24")

				w, h := int64(1024), int64(768)
				if t.Display != nil {
					if t.Display.Width > 0 {
						w = int64(t.Display.Width)
					}
					if t.Display.Height > 0 {
						h = int64(t.Display.Height)
					}
				}

				tools = append(tools, anthropic.BetaToolUnionParam{
					OfComputerUseTool20251124: &anthropic.BetaToolComputerUse20251124Param{
						DisplayWidthPx:  w,
						DisplayHeightPx: h,
					},
				})
				continue
			}
		}

		if t.Kind == provider.ToolKindShell {
			if t.Name == shell.NameBash || t.Name == "" {
				tools = append(tools, anthropic.BetaToolUnionParam{
					OfBashTool20250124: &anthropic.BetaToolBash20250124Param{},
				})
				continue
			}

			// OpenAI shell dialect — emulate as a function tool
			t = shell.FunctionTool(t)
		}

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

		// deferring requires a search tool to discover the definition, and
		// tools already discovered or called in prior turns must stay loaded
		if t.Deferred != nil && *t.Deferred && hasToolSearch && !discoveredNames[t.Name] && !usedNames[t.Name] {
			tool.DeferLoading = anthropic.Bool(true)
		}

		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}

	if options.Schema != nil && options.Schema.Properties != nil {
		req.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{
			Schema: ensureAdditionalPropertiesFalse(options.Schema.Properties),
		}
	}

	if options.CompactionOptions != nil && !isLegacyModel(c.model) {
		hasCompaction = true

		edit := &anthropic.BetaCompact20260112EditParam{}

		if options.CompactionOptions.Threshold > 0 {
			edit.Trigger = anthropic.BetaInputTokensTriggerParam{
				Value: int64(options.CompactionOptions.Threshold),
			}
		}

		req.ContextManagement = anthropic.BetaContextManagementConfigParam{
			Edits: []anthropic.BetaContextManagementConfigEditUnionParam{
				{OfCompact20260112: edit},
			},
		}
	}

	if hasCompaction {
		req.Betas = append(req.Betas, "compact-2026-01-12")
	}

	if len(system) > 0 {
		req.System = system
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
				p := &anthropic.BetaToolChoiceToolParam{
					Name: options.ToolOptions.Allowed[0],
				}

				if options.ToolOptions.DisableParallelToolCalls {
					p.DisableParallelToolUse = anthropic.Bool(true)
				}

				req.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfTool: p}
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

	if options.Temperature != nil && req.Thinking.OfAdaptive == nil {
		req.Temperature = anthropic.Float(float64(*options.Temperature))
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

		ReasoningTokens: int(usage.OutputTokensDetails.ThinkingTokens),

		CacheReadInputTokens:     int(usage.CacheReadInputTokens),
		CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
	}
}
