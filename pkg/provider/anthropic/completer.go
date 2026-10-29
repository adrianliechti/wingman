package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/claude"
	"github.com/adrianliechti/wingman/pkg/provider/internal/schema"
	"github.com/adrianliechti/wingman/pkg/provider/internal/toolid"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/adrianliechti/wingman/pkg/provider/tools/toolsearch"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
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

	sdkOptions, err := cfg.Options()
	if err != nil {
		return nil, err
	}

	return &Completer{
		Config:   cfg,
		messages: anthropic.NewBetaMessageService(sdkOptions...),
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		req, err := c.convertMessageRequest(messages, options)

		if err != nil {
			yield(nil, provider.InvalidRequest(err))
			return
		}

		// A paused turn (stop_reason pause_turn, raised by server tools) is
		// passed through as the native stop reason; the caller resends the
		// conversation to continue, as the API documents.
		continuations := assistantContinuations(messages)
		for {
			emitted, fallback, resume := false, false, false
			c.streamMessage(ctx, req, options, continuations)(func(delta *provider.Completion, err error) bool {
				if !emitted && req.Speed == anthropic.BetaMessageNewParamsSpeedFast && fastUnavailable(err) {
					fallback = true
					return false
				}
				if !emitted && prefillUnsupported(err) && canContinueAssistant(req, options) {
					resume = true
					return false
				}
				if delta != nil {
					emitted = true
				}
				return yield(delta, err)
			})
			if fallback {
				standard := *req
				standard.Speed = anthropic.BetaMessageNewParamsSpeedStandard
				req = &standard
				continue
			}
			if resume {
				last := len(req.Messages) - 1
				continuations = append(continuations, assistantContinuation{Message: last, Blocks: len(req.Messages[last].OfAssistant.Content)})
				continued := *req
				continued.Messages = append(slices.Clone(req.Messages), assistantContinuationMessage())
				req = &continued
				continue
			}
			return
		}
	}
}

func (c *Completer) streamMessage(ctx context.Context, req *anthropic.BetaMessageNewParams, options *provider.CompleteOptions, continuations []assistantContinuation) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		toolAliases := provider.ToolAliases(options.Tools)
		notes := progressNotes(req.Thinking) || (options.ReasoningOptions != nil && options.ReasoningOptions.IncludeSummary)

		message := anthropic.BetaMessage{}
		var requestOptions []option.RequestOption
		if req.Speed == anthropic.BetaMessageNewParamsSpeedFast {
			requestOptions = append(requestOptions, option.WithMaxRetries(0))
		}
		stream := c.messages.NewStreaming(ctx, *req, requestOptions...)
		defer stream.Close()
		messageStopped := false

		toolArgsSeen := map[int64]bool{}

		// Signatures arrive as fragments, while provider.Reasoning carries a
		// complete opaque value. Emit each signature once its block closes.
		pendingSignatures := map[int64]bool{}
		flushSignature := func(index int64) bool {
			if !pendingSignatures[index] {
				return true
			}
			delete(pendingSignatures, index)
			return yield(&provider.Completion{
				ID: message.ID, Model: c.model,
				Message: &provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: wrapContinuationSignature(message.Content[index].Signature, continuations)})},
				},
			}, nil)
		}

		// Text blocks form message items the way OpenAI's output does: adjacent
		// blocks (such as cited fragments) share one item, and a block that
		// follows a server tool or thinking block starts the next item.
		textItems := map[int64]string{}
		textItem, afterText := "", false

		// Emulated custom tools stream JSON-wrapped arguments. Their fragments
		// cannot be unwrapped one at a time, so buffer them per block and emit
		// the freeform text once the block closes.
		customArgs := map[int64]*strings.Builder{}

	messageStream:
		for stream.Next() {
			event := stream.Current()

			if err := message.Accumulate(event); err != nil {
				yield(nil, err)
				return
			}

			switch event := event.AsAny().(type) {
			case anthropic.BetaRawMessageStartEvent:
				if tier := effectiveTier(message.Usage); tier != "" {
					if !yield(&provider.Completion{ID: message.ID, Model: c.model, Tier: tier, Usage: toUsage(message.Usage)}, nil) {
						return
					}
				}
			case anthropic.BetaRawContentBlockStartEvent:
				startIndex := event.Index

				_, isText := event.ContentBlock.AsAny().(anthropic.BetaTextBlock)
				if isText {
					switch {
					case textItem == "":
						textItem = message.ID
					case !afterText:
						textItem = fmt.Sprintf("%s_%d", message.ID, startIndex)
					}
					textItems[startIndex] = textItem
				}
				afterText = isText

				switch event := event.ContentBlock.AsAny().(type) {
				case anthropic.BetaToolSearchToolResultBlock:
					result, err := ParseToolSearchResult(event.ToolUseID, []byte(event.Content.RawJSON()), options.Tools)
					if err != nil {
						yield(nil, err)
						return
					}
					if !yield(&provider.Completion{ID: message.ID, Model: c.model, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolResultContent(result)}}, Usage: toUsage(message.Usage)}, nil) {
						return
					}
				case anthropic.BetaThinkingBlock:
					pendingSignatures[startIndex] = event.Signature != ""
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ReasoningContent(thinkingReasoning(event.Thinking, "", notes)),
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
								{MessageID: textItems[startIndex], Text: event.Text},
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
									Signature: wrapContinuationSignature(event.Data, continuations),
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
								provider.CompactionContent(compactionFromBlock(message.ID, startIndex, event.Content, event.EncryptedContent, event.RawJSON())),
							},
						},

						Usage: toUsage(message.Usage),
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaToolUseBlock:
					call := provider.UnflattenToolCall(toolAliases, provider.ToolCall{
						ID:   event.ID,
						Name: event.Name,
					})

					if custom.IsEmulated(options.Tools, event.Name) {
						call.Kind = provider.ToolKindCustom
						customArgs[startIndex] = &strings.Builder{}
					}

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
								provider.ReasoningContent(thinkingReasoning(event.Thinking, "", notes)),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaSignatureDelta:
					if event.Signature != "" {
						pendingSignatures[blockIndex] = true
					}

				case anthropic.BetaCompactionContentBlockDelta:
					delta := &provider.Completion{
						ID:    message.ID,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.CompactionContent(compactionFromBlock(message.ID, blockIndex, event.Content, event.EncryptedContent, event.RawJSON())),
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
								{MessageID: textItems[blockIndex], Text: event.Text},
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case anthropic.BetaInputJSONDelta:
					if event.PartialJSON != "" {
						toolArgsSeen[blockIndex] = true
					}

					currentBlock := message.Content[blockIndex]

					// server-executed tools (tool search, web search) resolve
					// within the turn — their input must not leak as tool calls
					if currentBlock.Type != "tool_use" {
						break
					}

					if buffer, ok := customArgs[blockIndex]; ok {
						buffer.WriteString(event.PartialJSON)
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

					if !yield(delta, nil) {
						return
					}
				}

			case anthropic.BetaRawContentBlockStopEvent:
				if !flushSignature(event.Index) {
					return
				}

				block := &message.Content[event.Index]
				if block.Type == "tool_use" && !toolArgsSeen[event.Index] {
					// Keep input supplied in content_block_start. Only a function
					// with no input becomes {}; an empty custom input stays empty.
					arguments := string(block.Input)
					if buffer, custom := customArgs[event.Index]; custom {
						if arguments != "{}" {
							buffer.WriteString(arguments)
						}
					} else {
						if arguments == "" {
							arguments = "{}"
						}
						if !yield(&provider.Completion{
							ID: message.ID, Model: c.model,
							Message: &provider.Message{
								Role:    provider.MessageRoleAssistant,
								Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: block.ID, Arguments: arguments})},
							},
						}, nil) {
							return
						}
					}
				}
				if block.Type == "server_tool_use" && strings.HasPrefix(block.Name, "tool_search_tool_") {
					if !yield(&provider.Completion{
						ID:    message.ID,
						Model: c.model,
						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,
							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        block.ID,
									Name:      block.Name,
									Kind:      provider.ToolKindToolSearch,
									Execution: "server",
									Arguments: string(block.Input),
								}),
							},
						},
						Usage: toUsage(message.Usage),
					}, nil) {
						return
					}
				}

				buffer, ok := customArgs[event.Index]

				if !ok {
					break
				}

				delete(customArgs, event.Index)

				delta := &provider.Completion{
					ID:    message.ID,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								ID:        block.ID,
								Kind:      provider.ToolKindCustom,
								Arguments: custom.Unwrap(buffer.String()),
							}),
						},
					},
				}

				if !yield(delta, nil) {
					return
				}

			case anthropic.BetaRawMessageStopEvent:
				// A terminal message may arrive without every block_stop.
				for _, index := range slices.Sorted(maps.Keys(pendingSignatures)) {
					if !flushSignature(index) {
						return
					}
				}
				messageStopped = true
				if message.StopReason == "" {
					yield(nil, errors.New("anthropic: message_stop without a stop reason"))
					return
				}
				delta := &provider.Completion{
					ID:    message.ID,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},

					Tier:       effectiveTier(message.Usage),
					Usage:      toUsage(message.Usage),
					StopReason: provider.StopReason(message.StopReason),
				}

				// Most native reasons already use the provider spelling. Preserve
				// unknown values so new reasons do not fail otherwise valid turns.
				switch message.StopReason {
				case anthropic.BetaStopReasonStopSequence:
					delta.StopSequence = message.StopSequence
				case anthropic.BetaStopReasonModelContextWindowExceeded:
					delta.StopReason = provider.StopReasonContextExceeded
					delta.Status = provider.CompletionStatusIncomplete
				case anthropic.BetaStopReasonMaxTokens:
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
				break messageStream
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
		if !messageStopped {
			yield(nil, fmt.Errorf("anthropic: stream ended without message_stop: %w", io.ErrUnexpectedEOF))
			return
		}

		for _, blockIndex := range slices.Sorted(maps.Keys(customArgs)) {
			buffer := customArgs[blockIndex]
			if int(blockIndex) >= len(message.Content) {
				continue
			}

			block := message.Content[blockIndex]

			// Only a tool_use block carries a call id; anything else would
			// yield a tool call with an empty id.
			if block.Type != "tool_use" {
				continue
			}

			// The wrapper is unfinished here, so Unwrap would hand the raw
			// `{"input":"...` back as if it were freeform text.
			input, ok := custom.UnwrapPartial(buffer.String())

			if !ok {
				continue
			}

			delta := &provider.Completion{
				ID:    message.ID,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.ToolCallContent(provider.ToolCall{
							ID:        block.ID,
							Kind:      provider.ToolKindCustom,
							Arguments: input,
						}),
					},
				},
			}

			if !yield(delta, nil) {
				return
			}
		}
	}
}

func (c *Completer) convertMessageRequest(input []provider.Message, options *provider.CompleteOptions) (*anthropic.BetaMessageNewParams, error) {
	midSystem := claude.MatchesModel(c.model, []string{"fable-5", "mythos-5", "opus-4-8", "opus-5", "sonnet-5-5", "haiku-5"})
	if !midSystem {
		input = provider.ResolveInstructions(input)
	}
	// Sonnet's between_tools and Haiku's disabled thinking pin effort for
	// the conversation, so per-message updates are lowered to the request.
	disabledThinking := options != nil && options.ReasoningOptions != nil && options.ReasoningOptions.Type == provider.ReasoningTypeDisabled
	forcedTool := options != nil && options.ToolOptions != nil && options.ToolOptions.Choice == provider.ToolChoiceAny
	pinnedEffort := (disabledThinking && claude.MatchesModel(c.model, claude.BetweenToolsModels)) ||
		((disabledThinking || forcedTool) && claude.MatchesModel(c.model, []string{"haiku-5"}))
	if pinnedEffort || !claude.MatchesModel(c.model, []string{"fable-5-1", "mythos-5-1", "opus-5", "sonnet-5-5", "haiku-5"}) {
		input, options = provider.ResolveConfigurationUpdates(input, options)
	}
	if options == nil {
		options = new(provider.CompleteOptions)
	}
	// A compaction_trigger input item and the explicit option are equivalent.
	for i, message := range input {
		for j, content := range message.Content {
			if content.CompactionTrigger {
				if i != len(input)-1 || j != len(message.Content)-1 {
					return nil, fmt.Errorf("anthropic: compaction_trigger must be the final input item")
				}
				cloned := *options
				cloned.CompactionOptions = &provider.CompactionOptions{Trigger: true}
				options = &cloned
			}
		}
	}
	if options.CompactionOptions != nil && claude.MatchesModel(c.model, LegacyModels) {
		return nil, fmt.Errorf("anthropic: model %s does not support compaction", c.model)
	}

	// The whole stable prefix is cached automatically unless the client
	// caches explicitly, in which case only its breakpoints are marked.
	req := &anthropic.BetaMessageNewParams{
		Model: anthropic.Model(c.model),

		MaxTokens: 64000,
		Speed:     speed(options.Tier),
	}
	explicitCache := options.CacheOptions != nil && options.CacheOptions.Mode == provider.CacheModeExplicit
	if !explicitCache {
		req.CacheControl = cacheControl(options.CacheOptions, nil)
	}
	mark := cacheBreakpoints(input, explicitCache)

	if !claude.MatchesModel(c.model, LegacyModels) {
		req.MaxTokens = 128000
	}

	var system []anthropic.BetaTextBlockParam

	var tools []anthropic.BetaToolUnionParam
	var messages []anthropic.BetaMessageParam

	var hasCompaction, hasSignedCompaction bool

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
	discoveredNames := map[string]bool{}

	for _, m := range input {
		for _, content := range m.Content {
			if result := content.ToolResult; result != nil && result.Kind == provider.ToolKindToolSearch {
				found := toolsearch.Resolve(toolsearch.Tools(result.Payload), options.Tools)
				discovered = append(discovered, found...)
				if result.Execution == "client" {
					for _, tool := range provider.FlattenTools(found) {
						discoveredNames[tool.Name] = true
					}
				}
			}
		}
		var effort provider.Effort
		for _, content := range m.Content {
			if content.ConfigurationUpdate != nil {
				effort = content.ConfigurationUpdate.ReasoningEffort
			}
		}
		if effort != "" {
			if !slices.Contains(req.Betas, "mid-conversation-output-config-2026-07-01") {
				req.Betas = append(req.Betas, "mid-conversation-output-config-2026-07-01")
			}
			var blocks []anthropic.BetaSystemMessageParamContentUnion
			for _, content := range m.Content {
				if content.Text != "" {
					blocks = append(blocks, anthropic.BetaSystemMessageParamContentUnion{OfText: &anthropic.BetaTextBlockParam{Text: content.Text}})
				}
				if content.Instructions != nil {
					blocks = append(blocks, anthropic.BetaSystemMessageParamContentUnion{OfText: &anthropic.BetaTextBlockParam{Text: content.Instructions.Text}})
				}
			}
			update := anthropic.NewBetaSystemMessage(anthropic.BetaSystemMessageOutputConfigParam{
				Effort: anthropic.BetaSystemMessageOutputConfigEffort(outputEffort(effort)),
			}, blocks...)
			messages = append(messages, update)
			continue
		}
		switch m.Role {
		case provider.MessageRoleSystem:
			var texts []anthropic.BetaTextBlockParam
			var turnScoped bool

			for _, c := range m.Content {
				if c.Text != "" {
					block := anthropic.BetaTextBlockParam{Text: c.Text}
					if mark(c) {
						block.CacheControl = cacheControl(options.CacheOptions, c.CacheControl)
					}
					texts = append(texts, block)
				}
				if c.Instructions != nil {
					block := anthropic.BetaTextBlockParam{Text: c.Instructions.Text}
					if mark(c) {
						block.CacheControl = cacheControl(options.CacheOptions, c.CacheControl)
					}
					texts = append(texts, block)
					turnScoped = turnScoped || c.Instructions.Scope == provider.InstructionScopeTurn
				}
			}

			if len(texts) == 0 {
				break
			}

			if (len(messages) == 0 && !turnScoped) || !midSystem {
				system = append(system, texts...)
				break
			}

			blocks := make([]anthropic.BetaSystemMessageParamContentUnion, len(texts))
			for i := range texts {
				blocks[i] = anthropic.BetaSystemMessageParamContentUnion{OfText: &texts[i]}
			}

			message := anthropic.BetaSystemMessageParam{
				Content: blocks,
			}
			if turnScoped {
				message.ClearAt = anthropic.BetaSystemMessageParamClearAtNextUserMessage
				if !slices.Contains(req.Betas, "mid-conversation-system-clear-at-2026-08-21") {
					req.Betas = append(req.Betas, "mid-conversation-system-clear-at-2026-08-21")
				}
			}
			messages = append(messages, anthropic.BetaMessageParam{OfSystem: &message})

		case provider.MessageRoleUser:
			// tool_result blocks must precede other content in a user message
			var blocks []anthropic.BetaContentBlockParamUnion
			var contentBlocks []anthropic.BetaContentBlockParamUnion

			for _, c := range m.Content {
				if c.Compaction != nil {
					block, signed := compactionParam(c.Compaction)
					contentBlocks = append(contentBlocks, block)
					hasCompaction = true
					hasSignedCompaction = hasSignedCompaction || signed
				}
				if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
					block := anthropic.NewBetaTextBlock(text)
					if mark(c) {
						block.OfText.CacheControl = cacheControl(options.CacheOptions, c.CacheControl)
					}
					contentBlocks = append(contentBlocks, block)
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
						if c.ToolResult.Execution != "client" {
							// Responses represents hosted results as separate input
							// items; Claude keeps them in the assistant's turn.
							block := toolSearchResultBlock(*c.ToolResult)
							if len(messages) > 0 && messages[len(messages)-1].OfAssistant != nil {
								assistant := messages[len(messages)-1].OfAssistant
								assistant.Content = append(assistant.Content, block)
							} else {
								messages = append(messages, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{Content: []anthropic.BetaContentBlockParamUnion{block}}})
							}
							continue
						}

						result := &anthropic.BetaToolResultBlockParam{
							ToolUseID: toolid.Sanitize(c.ToolResult.ID, 128),
							Content: []anthropic.BetaToolResultBlockParamContentUnion{
								{OfText: &anthropic.BetaTextBlockParam{Text: string(c.ToolResult.Payload)}},
							},
						}
						if c.ToolResult.IsError {
							result.IsError = anthropic.Bool(true)
						}
						if mark(c) {
							result.CacheControl = cacheControl(options.CacheOptions, c.CacheControl)
						}

						blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
							OfToolResult: result,
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

					result := &anthropic.BetaToolResultBlockParam{
						ToolUseID: toolid.Sanitize(c.ToolResult.ID, 128),
						Content:   parts,
					}
					if c.ToolResult.IsError {
						result.IsError = anthropic.Bool(true)
					}
					if mark(c) {
						result.CacheControl = cacheControl(options.CacheOptions, c.CacheControl)
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfToolResult: result,
					})
				}
			}

			blocks = append(blocks, contentBlocks...)
			if len(blocks) == 0 {
				break
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
					signature, _ := unwrapContinuationSignature(c.Reasoning.Signature)
					// Include thinking blocks for conversation continuity.
					// Adaptive signatures carry the encrypted chain of thought,
					// so empty text still resumes reasoning on replay.
					if c.Reasoning.Redacted {
						blocks = append(blocks, anthropic.NewBetaRedactedThinkingBlock(signature))
					} else {
						// Responses-style clients replay the visible thinking as
						// a summary part; that text is what the signature covers.
						thinking := c.Reasoning.Text()
						if thinking == "" {
							thinking = c.Reasoning.SummaryText()
						}

						blocks = append(blocks, anthropic.NewBetaThinkingBlock(signature, thinking))
					}
				}

				if c.Compaction != nil && (c.Compaction.Content != "" || c.Compaction.Signature != "") {
					hasCompaction = true

					block, signed := compactionParam(c.Compaction)
					hasSignedCompaction = hasSignedCompaction || signed
					blocks = append(blocks, block)
				}

				if c.ToolCall != nil {
					if c.ToolCall.Kind == provider.ToolKindToolSearch && c.ToolCall.Execution != "client" {
						arguments := json.RawMessage(c.ToolCall.Arguments)
						if len(arguments) == 0 {
							arguments = json.RawMessage("{}")
						}
						blocks = append(blocks, anthropic.BetaContentBlockParamUnion{OfServerToolUse: &anthropic.BetaServerToolUseBlockParam{
							ID: toolSearchID(c.ToolCall.ID), Name: anthropic.BetaServerToolUseBlockParamName(toolSearchCallName(*c.ToolCall, options.Tools)), Input: arguments,
						}})
						continue
					}

					arguments := c.ToolCall.Arguments

					if c.ToolCall.Kind == provider.ToolKindCustom && !custom.IsWrapped(arguments) {
						// freeform input from the client — re-encode it the way
						// the emulated tool was declared, or the replay drops it
						arguments = custom.Wrap(arguments)
					}

					var input map[string]json.RawMessage

					if err := json.Unmarshal([]byte(arguments), &input); err != nil || input == nil {
						input = map[string]json.RawMessage{}
					}

					blocks = append(blocks, anthropic.BetaContentBlockParamUnion{
						OfToolUse: &anthropic.BetaToolUseBlockParam{
							ID:    toolid.Sanitize(c.ToolCall.ID, 128),
							Name:  provider.FlattenToolName(*c.ToolCall),
							Input: input,
						},
					})
				}
				if c.ToolResult != nil && c.ToolResult.Kind == provider.ToolKindToolSearch && c.ToolResult.Execution != "client" {
					blocks = append(blocks, toolSearchResultBlock(*c.ToolResult))
				}
			}

			// Filtering unsigned reasoning or unsupported content can leave an
			// empty assistant turn. After tool results this becomes a prefill,
			// which current Claude models reject.
			if len(blocks) == 0 {
				break
			}

			message := anthropic.BetaAssistantMessageParam{
				Content: blocks,
			}

			if len(messages) > 0 && messages[len(messages)-1].OfAssistant != nil {
				assistant := messages[len(messages)-1].OfAssistant
				assistant.Content = append(assistant.Content, message.Content...)
			} else {
				messages = append(messages, anthropic.BetaMessageParam{OfAssistant: &message})
			}
		}
	}

	toolList := provider.FlattenTools(options.Tools)

	defined := map[string]bool{}
	for _, t := range toolList {
		defined[t.Name] = true
	}

	for _, t := range provider.FlattenTools(discovered) {

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

		if t.Kind == provider.ToolKindCustom {
			// the Messages API has no freeform tool type — carry the text in a
			// single string parameter and unwrap it at the stream boundary
			t = custom.FunctionTool(t)
		}

		if t.Name == "" {
			continue
		}

		params := t.Parameters

		// strict validation rejects constraint keywords other providers accept
		if t.Strict != nil && *t.Strict {
			params = claude.SanitizeSchema(params)
		}

		// Keep opaque schema values raw: the SDK decoder uses float64 and
		// its encoder can encode json.Number nested in Properties as strings.
		type inputSchema anthropic.BetaToolInputSchemaParam
		var decoded struct {
			inputSchema
			Properties json.RawMessage `json:"properties"`
		}

		schemaData, err := json.Marshal(params)
		if err != nil {
			return nil, errors.New("invalid tool parameters schema")
		}

		if err := json.Unmarshal(schemaData, &decoded); err != nil {
			return nil, errors.New("invalid tool parameters schema")
		}
		schema := anthropic.BetaToolInputSchemaParam(decoded.inputSchema)
		if len(decoded.Properties) > 0 {
			schema.Properties = decoded.Properties
		}

		// Unmarshal only fills properties/required/type — carry all other
		// top-level keywords (additionalProperties, $defs, anyOf, ...) which
		// strict mode in particular depends on
		for key, value := range params {
			switch key {
			case "type", "properties", "required":
			default:
				if schema.ExtraFields == nil {
					schema.ExtraFields = map[string]any{}
				}
				data, _ := json.Marshal(value) // params was validated above
				schema.ExtraFields[key] = json.RawMessage(data)
			}
		}

		tool := anthropic.BetaToolParam{
			Name: t.Name,

			InputSchema: schema,
		}

		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}

		if t.Strict != nil {
			tool.Strict = anthropic.Bool(*t.Strict)
		}

		// Hosted results keep references in history. Keeping definitions
		// deferred also preserves the prefix covered by thinking signatures.
		if t.Deferred != nil && *t.Deferred && hasToolSearch && !discoveredNames[t.Name] {
			tool.DeferLoading = anthropic.Bool(true)
		}

		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}

	if options.Schema != nil && options.Schema.Properties != nil {
		req.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{
			Schema: schema.CloseObjects(claude.SanitizeSchema(options.Schema.Properties)),
		}
	} else if options.Schema != nil {
		// JSON mode without a schema (OpenAI json_object, Gemini
		// responseMimeType) has no native equivalent — instruct the model
		// instead so the output parses without markdown fences.
		system = append(system, anthropic.BetaTextBlockParam{Text: jsonModeInstruction})
	}

	triggerCompaction := options.CompactionOptions != nil && options.CompactionOptions.Trigger
	if !triggerCompaction && (options.CompactionOptions != nil || hasCompaction && !hasSignedCompaction) {
		if hasSignedCompaction {
			return nil, fmt.Errorf("anthropic: threshold compaction cannot be combined with a signed compaction block")
		}
		hasCompaction = true

		edit := &anthropic.BetaCompact20260112EditParam{}

		if options.CompactionOptions != nil && options.CompactionOptions.Threshold > 0 {
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

	if hasSignedCompaction || triggerCompaction {
		req.Betas = append(req.Betas, "compact-2026-09-04")
	} else if hasCompaction {
		req.Betas = append(req.Betas, "compact-2026-01-12")
	}

	if len(system) > 0 {
		req.System = system
	}

	if len(tools) > 0 {
		req.Tools = append(req.Tools, tools...)
	}

	forcesTool := false

	if options.ToolOptions != nil {
		switch options.ToolOptions.Choice {
		case provider.ToolChoiceNone:
			req.ToolChoice = anthropic.BetaToolChoiceUnionParam{
				OfNone: new(anthropic.NewBetaToolChoiceNoneParam()),
			}

		case provider.ToolChoiceAuto:
			p := &anthropic.BetaToolChoiceAutoParam{}

			if options.ToolOptions.DisableParallelToolCalls {
				p.DisableParallelToolUse = anthropic.Bool(true)
			}

			req.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfAuto: p}

		case provider.ToolChoiceAny:
			if claude.MatchesModel(c.model, claude.NoForcedToolChoiceModels) {
				return nil, fmt.Errorf("anthropic: model %s does not support forced tool_choice; use auto or none", c.model)
			}

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
	}

	thinking := c.resolveThinking(options, forcesTool)

	if thinking.Enabled {
		display := anthropic.BetaThinkingConfigAdaptiveDisplayOmitted
		switch {
		case thinking.Summarized:
			display = anthropic.BetaThinkingConfigAdaptiveDisplaySummarized
		case thinking.Updates:
			display = anthropic.BetaThinkingConfigAdaptiveDisplayUpdates
			req.Betas = append(req.Betas, anthropic.AnthropicBetaThinkingDisplayUpdates2026_08_18)
		}

		req.Thinking = anthropic.BetaThinkingConfigParamUnion{
			OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{Display: display},
		}
	} else if thinking.Disabled && claude.MatchesModel(c.model, claude.BetweenToolsModels) {
		req.Thinking = anthropic.BetaThinkingConfigParamUnion{
			OfBetweenTools: &anthropic.BetaThinkingConfigBetweenToolsParam{},
		}
	} else if thinking.Disabled {
		req.Thinking = anthropic.BetaThinkingConfigParamUnion{
			OfDisabled: &anthropic.BetaThinkingConfigDisabledParam{},
		}
	}
	// Retention is meaningful only with active thinking; a forced tool call
	// can disable it above.
	activeThinking := thinking.Enabled || claude.MatchesModel(c.model, claude.AlwaysThinkingModels) ||
		(!thinking.Disabled && claude.MatchesModel(c.model, claude.DefaultThinkingModels))
	if options.ReasoningOptions != nil && activeThinking {
		var keep anthropic.BetaClearThinking20251015EditKeepUnionParam
		switch options.ReasoningOptions.Context {
		case provider.ReasoningContextAllTurns:
			keep.OfAll = "all"
		case provider.ReasoningContextCurrentTurn:
			keep.OfThinkingTurns = &anthropic.BetaThinkingTurnsParam{Value: 1}
		}
		if keep.OfAll != "" || keep.OfThinkingTurns != nil {
			// Thinking retention precedes compaction when both are requested.
			req.ContextManagement.Edits = append([]anthropic.BetaContextManagementConfigEditUnionParam{
				{OfClearThinking20251015: &anthropic.BetaClearThinking20251015EditParam{Keep: keep}},
			}, req.ContextManagement.Edits...)
			req.Betas = append(req.Betas, "context-management-2025-06-27")
		}
	}

	if thinking.Effort != "" {
		req.OutputConfig.Effort = thinking.Effort
	}

	if len(messages) > 0 {
		var err error
		req.Messages, err = replayAssistantContinuations(messages, assistantContinuations(input))
		if err != nil {
			return nil, err
		}
	}
	if triggerCompaction {
		if len(options.Stop) > 0 || options.Schema != nil || forcesTool {
			return nil, fmt.Errorf("anthropic: compaction cannot be combined with stop sequences, output format, or forced tools")
		}
		compaction := map[string]any{"type": "summarize"}
		req.SetExtraFields(map[string]any{"compaction": compaction})
	}

	return req, nil
}

func toUsage(usage anthropic.BetaUsage) *provider.Usage {
	var reasoningTokens *int
	if usage.OutputTokensDetails.JSON.ThinkingTokens.Valid() || usage.OutputTokensDetails.ThinkingTokens > 0 {
		reasoningTokens = new(int(usage.OutputTokensDetails.ThinkingTokens))
	}

	if usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.CacheReadInputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 && len(usage.Iterations) == 0 && reasoningTokens == nil {
		return nil
	}

	cacheReadInputTokens := int(usage.CacheReadInputTokens)
	cacheCreationInputTokens := int(usage.CacheCreationInputTokens)

	result := &provider.Usage{
		// Anthropic reports input_tokens excluding cached tokens. Normalize to a
		// cache-inclusive total so the intermediate Usage has one consistent
		// meaning across providers (cache fields are the cached subset of it).
		InputTokens:  int(usage.InputTokens) + cacheReadInputTokens + cacheCreationInputTokens,
		OutputTokens: int(usage.OutputTokens),

		ReasoningTokens: reasoningTokens,

		CacheReadInputTokens:     cacheReadInputTokens,
		CacheCreationInputTokens: cacheCreationInputTokens,
	}
	if usage.JSON.CacheCreation.Valid() {
		result.MergeCacheWrites(cacheCreationInputTokens, &provider.CacheWriteTokensDetails{
			StandardTokens: int(usage.CacheCreation.Ephemeral5mInputTokens),
			ExtendedTokens: int(usage.CacheCreation.Ephemeral1hInputTokens),
		})
	}
	if len(usage.Iterations) > 0 {
		// Top-level usage excludes compaction. Keep the shared usage contract:
		// total billed tokens, with cached tokens included in the input total.
		result.InputTokens, result.OutputTokens = 0, 0
		result.CacheReadInputTokens, result.CacheCreationInputTokens = 0, 0
		result.CacheWriteTokensDetails = nil
		for _, iteration := range usage.Iterations {
			input := int(iteration.InputTokens + iteration.CacheReadInputTokens + iteration.CacheCreationInputTokens)
			result.InputTokens += input
			result.OutputTokens += int(iteration.OutputTokens)
			result.CacheReadInputTokens += int(iteration.CacheReadInputTokens)
			result.CacheCreationInputTokens += int(iteration.CacheCreationInputTokens)
		}
	}
	return result
}

func compactionFromBlock(messageID string, index int64, content, encrypted, raw string) provider.Compaction {
	var block struct {
		Signature string `json:"signature"`
	}
	_ = json.Unmarshal([]byte(raw), &block)
	result := provider.Compaction{ID: fmt.Sprintf("%s_compaction_%d", messageID, index), Content: content, Signature: encrypted}
	if block.Signature != "" {
		result.Signature = WrapCompactionSignature(block.Signature)
	}
	return result
}
