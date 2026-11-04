package bedrock

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

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config

	client *bedrockruntime.Client
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model:  model,
		client: provider.DefaultClient,
	}

	for _, option := range options {
		option(cfg)
	}

	var configOptions []func(*config.LoadOptions) error

	if cfg.client != nil {
		configOptions = append(configOptions, config.WithHTTPClient(cfg.client))
	}

	config, err := config.LoadDefaultConfig(context.Background(), configOptions...)

	if err != nil {
		return nil, err
	}

	client := bedrockruntime.NewFromConfig(config)

	return &Completer{
		Config: cfg,

		client: client,
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		messages, options = c.resolveInput(messages, options)

		req, err := c.convertConverseInput(messages, options)

		if err != nil {
			yield(nil, provider.InvalidRequest(err))
			return
		}

		_, thinking := c.converseAdditionalFields(options)
		notes := c.progressNotes(thinking) || thinking.Summarized

		params := &bedrockruntime.ConverseStreamInput{
			ModelId: req.ModelId,

			Messages: req.Messages,

			System:     req.System,
			ToolConfig: req.ToolConfig,

			InferenceConfig: req.InferenceConfig,
			OutputConfig:    req.OutputConfig,
			ServiceTier:     req.ServiceTier,

			AdditionalModelRequestFields: req.AdditionalModelRequestFields,
		}

		resp, err := c.client.ConverseStream(ctx, params)

		if err != nil {
			yield(nil, convertError(err))
			return
		}
		stream := resp.GetStream()
		defer stream.Close()
		messageStopped := false

		id := uuid.NewString()

		toolAliases := provider.ToolAliases(options.Tools)
		toolCallIDs := map[int32]string{}
		toolArgsSeen := map[int32]bool{}

		// The shared Reasoning signature is a whole opaque value; Converse
		// signature deltas must be joined before it reaches an accumulator.
		signatures := map[int32]string{}
		flushSignature := func(index int32) bool {
			signature := signatures[index]
			delete(signatures, index)
			if signature == "" {
				return true
			}
			return yield(&provider.Completion{
				ID: id, Model: c.model,
				Message: &provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: signature})},
				},
			}, nil)
		}

		// Schema mode emulates structured output with a forced tool; its
		// blocks stream as text while every other tool call stays a call.
		schemaBlocks := map[int32]bool{}
		sawToolCall := false

		// Emulated custom tools stream JSON-wrapped arguments. Their fragments
		// cannot be unwrapped one at a time, so buffer them per block and emit
		// the freeform text once the block closes.
		customArgs := map[int32]*strings.Builder{}

		// Adjacent text blocks share an item; reasoning and tool blocks
		// separate them so replay preserves their order. Converse streams
		// blocks sequentially, so only the current text item is needed.
		textItem, afterText := "", false

		for event := range stream.Events() {
			switch v := event.(type) {
			case *types.ConverseStreamOutputMemberMessageStart:
				delta := &provider.Completion{
					ID:    id,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,
					},
				}

				if !yield(delta, nil) {
					return
				}

			case *types.ConverseStreamOutputMemberContentBlockStart:
				afterText = false

				switch b := v.Value.Start.(type) {
				case *types.ContentBlockStartMemberToolUse:
					toolCallIDs[aws.ToInt32(v.Value.ContentBlockIndex)] = aws.ToString(b.Value.ToolUseId)

					// The schema tool's call surfaces as text via the argument
					// deltas, so there is nothing to emit at block start.
					if c.schemaAsTool(options) && aws.ToString(b.Value.Name) == options.Schema.Name {
						schemaBlocks[aws.ToInt32(v.Value.ContentBlockIndex)] = true
						continue
					}

					sawToolCall = true

					call := provider.UnflattenToolCall(toolAliases, provider.ToolCall{
						ID:   aws.ToString(b.Value.ToolUseId),
						Name: aws.ToString(b.Value.Name),
					})

					if custom.IsEmulated(options.Tools, aws.ToString(b.Value.Name)) {
						call.Kind = provider.ToolKindCustom
						customArgs[aws.ToInt32(v.Value.ContentBlockIndex)] = &strings.Builder{}
					}

					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(call),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				default:
					// Unknown block type, skip silently
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				blockIndex := aws.ToInt32(v.Value.ContentBlockIndex)
				_, isText := v.Value.Delta.(*types.ContentBlockDeltaMemberText)
				isText = isText || schemaBlocks[blockIndex]
				if isText {
					switch {
					case textItem == "":
						textItem = "msg_" + id
					case !afterText:
						textItem = fmt.Sprintf("msg_%s_%d", id, blockIndex)
					}
				}
				afterText = isText

				switch b := v.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberReasoningContent:
					switch r := b.Value.(type) {
					case *types.ReasoningContentBlockDeltaMemberText:
						delta := &provider.Completion{
							ID:    id,
							Model: c.model,

							Message: &provider.Message{
								Role: provider.MessageRoleAssistant,

								Content: []provider.Content{
									provider.ReasoningContent(thinkingReasoning(r.Value, notes)),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}

					case *types.ReasoningContentBlockDeltaMemberSignature:
						signatures[blockIndex] += r.Value

					case *types.ReasoningContentBlockDeltaMemberRedactedContent:
						delta := &provider.Completion{
							ID:    id,
							Model: c.model,

							Message: &provider.Message{
								Role: provider.MessageRoleAssistant,

								Content: []provider.Content{
									provider.ReasoningContent(provider.Reasoning{
										Signature: base64.StdEncoding.EncodeToString(r.Value),
										Redacted:  true,
									}),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}
					}

				case *types.ContentBlockDeltaMemberText:
					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								{MessageID: textItem, Text: b.Value},
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case *types.ContentBlockDeltaMemberToolUse:
					if aws.ToString(b.Value.Input) != "" {
						toolArgsSeen[aws.ToInt32(v.Value.ContentBlockIndex)] = true
					}

					if buffer, ok := customArgs[aws.ToInt32(v.Value.ContentBlockIndex)]; ok {
						buffer.WriteString(aws.ToString(b.Value.Input))
						continue
					}

					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        toolCallIDs[aws.ToInt32(v.Value.ContentBlockIndex)],
									Arguments: aws.ToString(b.Value.Input),
								}),
							},
						},
					}

					// Schema mode: the schema tool's arguments are the answer.
					if schemaBlocks[aws.ToInt32(v.Value.ContentBlockIndex)] {
						delta.Message.Content = []provider.Content{
							{MessageID: textItem, Text: aws.ToString(b.Value.Input)},
						}
					}

					if !yield(delta, nil) {
						return
					}

				default:
					// Unknown delta type, skip silently
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:
				// Tool use blocks without input deltas would otherwise
				// accumulate empty arguments downstream — normalize to "{}"
				blockIndex := aws.ToInt32(v.Value.ContentBlockIndex)
				if !flushSignature(blockIndex) {
					return
				}

				if buffer, ok := customArgs[blockIndex]; ok {
					delete(customArgs, blockIndex)

					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        toolCallIDs[blockIndex],
									Kind:      provider.ToolKindCustom,
									Arguments: custom.Unwrap(buffer.String()),
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

					continue
				}

				if callID, ok := toolCallIDs[blockIndex]; ok && !toolArgsSeen[blockIndex] && !schemaBlocks[blockIndex] {
					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:        callID,
									Arguments: "{}",
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				for _, index := range slices.Sorted(maps.Keys(signatures)) {
					if !flushSignature(index) {
						return
					}
				}
				messageStopped = true
				if v.Value.StopReason == "" {
					yield(nil, errors.New("bedrock: messageStop without a stop reason"))
					return
				}
				// Bedrock reports unusable generations as stop reasons. Their
				// content, including any tool call, must not reach the client.
				switch v.Value.StopReason {
				case types.StopReasonMalformedToolUse, types.StopReasonMalformedModelOutput:
					yield(nil, fmt.Errorf("bedrock: model output unusable: %s", v.Value.StopReason))
					return
				}
				delta := &provider.Completion{
					ID:    id,
					Model: c.model,

					StopReason: provider.StopReason(v.Value.StopReason),

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(""),
						},
					},
				}

				// Preserve new native reasons; normalize only known differences.
				switch v.Value.StopReason {
				case types.StopReasonToolUse:
					// the forced schema tool is not a tool call to the client
					if len(schemaBlocks) > 0 && !sawToolCall {
						delta.StopReason = provider.StopReasonEndTurn
					}
				case types.StopReasonMaxTokens:
					delta.Status = provider.CompletionStatusIncomplete
				case types.StopReasonModelContextWindowExceeded:
					delta.StopReason = provider.StopReasonContextExceeded
					delta.Status = provider.CompletionStatusIncomplete
				case types.StopReasonGuardrailIntervened, types.StopReasonContentFiltered:
					delta.StopReason = provider.StopReasonRefusal
					delta.Status = provider.CompletionStatusRefused
				}

				if !yield(delta, nil) {
					return
				}

			case *types.ConverseStreamOutputMemberMetadata:
				delta := &provider.Completion{
					ID:    id,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(""),
						},
					},

					Usage: toUsage(v.Value.Usage),
				}
				if v.Value.ServiceTier != nil {
					delta.Tier = tierFromServiceTier(v.Value.ServiceTier.Type)
				}

				if !yield(delta, nil) {
					return
				}

			case *types.UnknownUnionMember:
				// Unknown union member, skip silently

			default:
				// Unknown event type, skip silently
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
		if !messageStopped {
			yield(nil, fmt.Errorf("bedrock: stream ended without messageStop: %w", io.ErrUnexpectedEOF))
			return
		}

		for _, blockIndex := range slices.Sorted(maps.Keys(customArgs)) {
			buffer := customArgs[blockIndex]
			// The wrapper is unfinished here, so Unwrap would hand the raw
			// `{"input":"...` back as if it were freeform text.
			input, ok := custom.UnwrapPartial(buffer.String())

			if !ok {
				continue
			}

			delta := &provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.ToolCallContent(provider.ToolCall{
							ID:        toolCallIDs[blockIndex],
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

// convertError extracts meaningful error information from AWS SDK errors
func convertError(err error) error {
	if err == nil {
		return nil
	}

	wrap := func(statusCode int, msg string, errorType ...string) error {
		var typeName string
		if len(errorType) > 0 {
			typeName = errorType[0]
		}
		return &provider.ProviderError{
			Code:    statusCode,
			Type:    typeName,
			Message: msg,
			Err:     err,
		}
	}

	var throttle *types.ThrottlingException
	if errors.As(err, &throttle) {
		return wrap(429, fmt.Sprintf("bedrock throttling: %s", aws.ToString(throttle.Message)))
	}

	var quotaExceeded *types.ServiceQuotaExceededException
	if errors.As(err, &quotaExceeded) {
		return wrap(429, fmt.Sprintf("bedrock quota exceeded: %s", aws.ToString(quotaExceeded.Message)))
	}

	var validation *types.ValidationException
	if errors.As(err, &validation) {
		message := aws.ToString(validation.Message)
		errorType := ""
		if isBedrockContentFilterMessage(message) {
			errorType = "content_filter"
		}
		return wrap(400, fmt.Sprintf("bedrock validation: %s", message), errorType)
	}

	var accessDenied *types.AccessDeniedException
	if errors.As(err, &accessDenied) {
		return wrap(403, fmt.Sprintf("bedrock access denied: %s", aws.ToString(accessDenied.Message)))
	}

	var resourceNotFound *types.ResourceNotFoundException
	if errors.As(err, &resourceNotFound) {
		return wrap(404, fmt.Sprintf("bedrock resource not found: %s", aws.ToString(resourceNotFound.Message)))
	}

	var conflict *types.ConflictException
	if errors.As(err, &conflict) {
		return wrap(409, fmt.Sprintf("bedrock conflict: %s", aws.ToString(conflict.Message)))
	}

	var modelNotReady *types.ModelNotReadyException
	if errors.As(err, &modelNotReady) {
		return wrap(503, fmt.Sprintf("bedrock model not ready: %s", aws.ToString(modelNotReady.Message)))
	}

	var serviceUnavailable *types.ServiceUnavailableException
	if errors.As(err, &serviceUnavailable) {
		return wrap(503, fmt.Sprintf("bedrock service unavailable: %s", aws.ToString(serviceUnavailable.Message)))
	}

	var modelTimeout *types.ModelTimeoutException
	if errors.As(err, &modelTimeout) {
		return wrap(504, fmt.Sprintf("bedrock model timeout: %s", aws.ToString(modelTimeout.Message)))
	}

	var internalServer *types.InternalServerException
	if errors.As(err, &internalServer) {
		return wrap(500, fmt.Sprintf("bedrock internal error: %s", aws.ToString(internalServer.Message)))
	}

	var modelErr *types.ModelStreamErrorException
	if errors.As(err, &modelErr) {
		status := 500
		if modelErr.OriginalStatusCode != nil && *modelErr.OriginalStatusCode > 0 {
			status = int(*modelErr.OriginalStatusCode)
		}
		return wrap(status, fmt.Sprintf("bedrock stream error: %s", aws.ToString(modelErr.Message)))
	}

	var modelError *types.ModelErrorException
	if errors.As(err, &modelError) {
		status := 500
		if modelError.OriginalStatusCode != nil && *modelError.OriginalStatusCode > 0 {
			status = int(*modelError.OriginalStatusCode)
		}
		return wrap(status, fmt.Sprintf("bedrock model error: %s", aws.ToString(modelError.Message)))
	}

	// Non-modeled HTTP errors: preserve the real upstream status code.
	var respErr *http.ResponseError
	if errors.As(err, &respErr) {
		status := respErr.HTTPStatusCode()
		if status <= 0 {
			status = 500
		}
		return wrap(status, fmt.Sprintf("bedrock: %s", err.Error()))
	}

	var ae smithy.APIError
	if errors.As(err, &ae) {
		status := 500
		if ae.ErrorFault() == smithy.FaultClient {
			status = 400
		}
		return wrap(status, fmt.Sprintf("bedrock error [%s]: %s", ae.ErrorCode(), ae.ErrorMessage()))
	}

	return err
}

func isBedrockContentFilterMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "content filter") &&
		(strings.Contains(message, "blocked") || strings.Contains(message, "filtered"))
}

// converseAdditionalFields builds the Anthropic request fields Converse has
// no native mapping for. The effort stays here as well: the typed
// outputConfig.effort field is rejected by Claude 4.6 ("This model doesn't
// support the effort field"), while output_config.effort in the additional
// fields is accepted. The resolved thinking configuration is returned for
// selecting reasoning output in the stream.
func (c *Completer) converseAdditionalFields(options *provider.CompleteOptions) (map[string]any, thinking) {
	// Forced tool calls (emulated schema mode, tool choice "any") are
	// incompatible with thinking on Anthropic models over Bedrock. Models
	// without forced tool choice steer the schema tool automatically instead.
	forced := !claude.MatchesModel(c.model, claude.NoForcedToolChoiceModels) && (c.schemaAsTool(options) ||
		(options.ToolOptions != nil && options.ToolOptions.Choice == provider.ToolChoiceAny))

	thinking := c.resolveThinking(options, forced)

	fields := map[string]any{}

	if thinking.Enabled {
		// Always explicit: Claude 5.x models omit thinking text by default.
		display := "omitted"
		switch {
		case thinking.Summarized:
			display = "summarized"
		case thinking.Updates:
			display = "updates"
			fields["anthropic_beta"] = []string{"thinking-display-updates-2026-08-18"}
		}

		fields["thinking"] = map[string]any{"type": "adaptive", "display": display}
	} else if thinking.Disabled && claude.MatchesModel(c.model, claude.BetweenToolsModels) {
		fields["thinking"] = map[string]any{"type": "between_tools"}
	} else if thinking.Disabled && claude.MatchesModel(c.model, claude.DefaultThinkingModels) {
		// Bedrock only accepts the explicit disable on models that think
		// by default; the others are off when the field is omitted.
		fields["thinking"] = map[string]any{"type": "disabled"}
	}

	if thinking.Effort != "" {
		fields["output_config"] = map[string]any{"effort": thinking.Effort}
	}

	return fields, thinking
}

// schemaAsTool reports whether schema mode is emulated with a forced tool
// call: the model has no native JSON-schema output, the request asks for JSON
// without a schema, or a non-strict schema allows arbitrary object keys.
func (c *Completer) schemaAsTool(options *provider.CompleteOptions) bool {
	if options.Schema == nil {
		return false
	}
	if options.Schema.Properties == nil || !supportsOutputFormat(c.model) {
		return true
	}
	// Native grammars reject dictionaries. Preserve their schema with the
	// existing non-strict tool emulation instead of closing or rejecting them.
	return (options.Schema.Strict == nil || !*options.Schema.Strict) && schema.AllowsAdditionalProperties(options.Schema.Properties)
}

// resolveInput lowers the shared conversation features Converse has no
// positional form for: instruction lifetimes, effort updates, and hosted
// tool search all fall back to the request level.
func (c *Completer) resolveInput(messages []provider.Message, options *provider.CompleteOptions) ([]provider.Message, *provider.CompleteOptions) {
	messages = provider.ResolveInstructions(messages)
	messages, options = provider.ResolveConfigurationUpdates(messages, options)

	return toolsearch.Inline(messages, options)
}

func (c *Completer) convertConverseInput(input []provider.Message, options *provider.CompleteOptions) (*bedrockruntime.ConverseInput, error) {
	if options != nil && options.ReasoningOptions != nil {
		mode := options.ReasoningOptions.Context
		// Converse cannot configure retention. Claude Code's keep-all request
		// is already satisfied by models that preserve all thinking by default.
		keepAll := mode == provider.ReasoningContextAllTurns && claude.MatchesModel(c.model, PreservedThinkingModels)
		if mode != "" && mode != provider.ReasoningContextAuto && !keepAll {
			return nil, &provider.ProviderError{
				Code:    400,
				Type:    "invalid_request_error",
				Message: "Bedrock Converse cannot change this model's reasoning.context; omit it or use auto",
			}
		}
	}
	input, options = c.resolveInput(input, options)

	cache := newCachePolicy(input, options)
	messages, err := c.convertMessagesPolicy(input, cache)

	if err != nil {
		return nil, err
	}

	// Converse has no "none" choice, and Claude's native one leaves current
	// models with an empty answer where they would have called a tool. The
	// tools are left out instead. Replayed tool blocks need their definitions,
	// so with tool history "none" is best effort.
	toolOptions := options.ToolOptions
	none := toolOptions != nil && toolOptions.Choice == provider.ToolChoiceNone
	hasToolHistory := inputHasToolBlocks(input)

	if none && hasToolHistory {
		toolOptions = nil
	}

	tools := provider.FlattenTools(options.Tools)
	if hasToolHistory {
		tools = withHistoryTools(tools, input)
	}

	config, err := c.convertToolConfigPolicy(tools, toolOptions, cache)

	if err != nil {
		return nil, err
	}

	var output *types.OutputConfig

	// Schema mode: models with structured outputs take the schema natively
	// as outputConfig.textFormat; the grammar is enforced, so the schema must
	// meet the strict-mode subset like a strict tool does. Everything else
	// exposes the schema as a tool and forces its use.
	if options.Schema != nil && !c.schemaAsTool(options) {
		properties := schema.CloseObjects(claude.SanitizeSchema(options.Schema.Properties))

		data, err := json.Marshal(properties)

		if err != nil {
			return nil, err
		}

		definition := types.JsonSchemaDefinition{
			Schema: aws.String(string(data)),
		}

		if options.Schema.Name != "" {
			definition.Name = aws.String(options.Schema.Name)
		}

		if options.Schema.Description != "" {
			definition.Description = aws.String(options.Schema.Description)
		}

		output = &types.OutputConfig{
			TextFormat: &types.OutputFormat{
				Type: types.OutputFormatTypeJsonSchema,

				Structure: &types.OutputFormatStructureMemberJsonSchema{
					Value: definition,
				},
			},
		}
	} else if options.Schema != nil {
		if config == nil {
			config = &types.ToolConfiguration{}
		}

		tool := types.ToolSpecification{
			Name: aws.String(options.Schema.Name),
		}

		if options.Schema.Description != "" {
			tool.Description = aws.String(options.Schema.Description)
		}

		properties := options.Schema.Properties
		if properties == nil {
			properties = map[string]any{"type": "object"}
		}

		// Only forward strict when explicitly enabled and the model supports it:
		// strict=false carries no information, and models without structured
		// output reject the field itself. Strict mode also requires
		// additionalProperties: false on every object and rejects constraint
		// keywords other providers accept.
		if options.Schema.Strict != nil && *options.Schema.Strict && supportsStrictTools(c.model) {
			tool.Strict = options.Schema.Strict
			properties = schema.CloseObjects(claude.SanitizeSchema(properties))
		}

		tool.InputSchema = &types.ToolInputSchemaMemberJson{
			Value: newDocument(properties),
		}

		if claude.MatchesModel(c.model, claude.NoForcedToolChoiceModels) {
			// The model rejects forced tool choice: steer toward the schema
			// tool from its description and let the model select it.
			tool.Description = aws.String(strings.TrimSpace(aws.ToString(tool.Description) + " " + schemaToolInstruction))
		}

		config.Tools = append(config.Tools, &types.ToolMemberToolSpec{Value: tool})

		if claude.MatchesModel(c.model, claude.NoForcedToolChoiceModels) {
			config.ToolChoice = &types.ToolChoiceMemberAuto{}
		} else if len(config.Tools) > 1 && !none {
			// Client tools stay callable: the model must call some tool, and
			// the schema tool is the only way to produce the final answer.
			config.ToolChoice = &types.ToolChoiceMemberAny{}
		} else {
			// Forcing the schema tool also keeps "none" with tool history.
			config.ToolChoice = &types.ToolChoiceMemberTool{
				Value: types.SpecificToolChoice{
					Name: aws.String(options.Schema.Name),
				},
			}
		}
	}

	inference := &types.InferenceConfiguration{}

	if tokens := defaultMaxTokens(c.model); tokens > 0 {
		inference.MaxTokens = aws.Int32(tokens)
	}

	if options.MaxTokens != nil {
		inference.MaxTokens = aws.Int32(int32(*options.MaxTokens))
	}

	if len(options.Stop) > 0 {
		inference.StopSequences = options.Stop
	}

	req := &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.model),

		Messages: messages,

		System:     c.convertSystemPolicy(input, cache),
		ToolConfig: config,

		InferenceConfig: inference,
		OutputConfig:    output,
		ServiceTier:     serviceTier(options.Tier),
	}

	fields, _ := c.converseAdditionalFields(options)
	if len(options.Tools) > 0 && config != nil && options.ToolOptions != nil && options.ToolOptions.DisableParallelToolCalls && !none && isClaudeModel(c.model) {
		if config.ToolChoice == nil {
			config.ToolChoice = &types.ToolChoiceMemberAuto{}
		}
		// Converse supplies the type/name from ToolConfig. Repeating those
		// fields here conflicts with it; only forward Claude's extra control.
		fields["tool_choice"] = map[string]any{"disable_parallel_tool_use": true}
	}

	if len(fields) > 0 {
		req.AdditionalModelRequestFields = newDocument(fields)
	}

	return req, nil
}

// Converse only supports the top-level system field. Mid-conversation system
// messages require InvokeModel, even on newer Claude models. Collect the
// resolved instructions here so a trailing system message cannot make an
// otherwise valid user/tool-result turn fail Bedrock's last-turn validation.
func (c *Completer) convertSystem(messages []provider.Message) []types.SystemContentBlock {
	return c.convertSystemPolicy(messages, cachePolicy{})
}

func (c *Completer) convertSystemPolicy(messages []provider.Message, cache cachePolicy) []types.SystemContentBlock {
	var result []types.SystemContentBlock

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			continue
		}

		for _, content := range m.Content {
			if content.Text == "" {
				continue
			}

			system := &types.SystemContentBlockMemberText{
				Value: content.Text,
			}

			result = append(result, system)
			if cache.marks(content) {
				result = append(result, &types.SystemContentBlockMemberCachePoint{Value: cache.point(content.CacheControl)})
			}
		}
	}

	if len(result) == 0 {
		return nil
	}

	// Add cache point after system messages for Claude models
	if isClaudeModel(c.model) && !cache.explicit {
		result = append(result, &types.SystemContentBlockMemberCachePoint{
			Value: cache.point(nil),
		})
	}

	return result
}

// convertMessages builds the conversation without system messages, whose
// resolved instructions are collected by convertSystem.
func (c *Completer) convertMessages(messages []provider.Message) ([]types.Message, error) {
	return c.convertMessagesPolicy(messages, cachePolicy{})
}

func (c *Completer) convertMessagesPolicy(messages []provider.Message, cache cachePolicy) ([]types.Message, error) {
	var result []types.Message

	// Merge consecutive turns after hoisting system instructions.
	var merged []provider.Message
	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].Role == m.Role {
			last := &merged[len(merged)-1]
			last.Content = append(last.Content, m.Content...)
		} else {
			m.Content = slices.Clone(m.Content)
			merged = append(merged, m)
		}
	}

	for _, m := range merged {
		var err error

		var role types.ConversationRole
		var content []types.ContentBlock

		switch m.Role {
		case provider.MessageRoleUser:
			role = types.ConversationRoleUser
			content, err = convertUserContentPolicy(m, cache)

		case provider.MessageRoleAssistant:
			role = types.ConversationRoleAssistant
			content, err = convertAssistantContent(m)

		default:
			return nil, errors.New("unsupported message role")
		}

		if err != nil {
			return nil, err
		}

		if len(content) == 0 {
			continue
		}

		if len(result) > 0 && result[len(result)-1].Role == role {
			last := &result[len(result)-1]
			if role == types.ConversationRoleUser {
				last.Content = mergeUserBlocks(last.Content, content)
			} else {
				last.Content = append(last.Content, content...)
			}
			continue
		}

		result = append(result, types.Message{
			Role:    role,
			Content: content,
		})
	}

	// Add cache point to the last user message for Claude models
	if isClaudeModel(c.model) && !cache.explicit && len(result) > 0 {
		for i := len(result) - 1; i >= 0; i-- {
			if result[i].Role == types.ConversationRoleUser {
				result[i].Content = appendMessageCachePoint(result[i].Content, cache.point(nil))
				break
			}
		}
	}

	return result, nil
}

func mergeUserBlocks(a, b []types.ContentBlock) []types.ContentBlock {
	var results, content []types.ContentBlock
	for _, list := range [][]types.ContentBlock{a, b} {
		for i := 0; i < len(list); i++ {
			if _, ok := list[i].(*types.ContentBlockMemberToolResult); ok {
				results = append(results, list[i])
				if i+1 < len(list) {
					if _, cp := list[i+1].(*types.ContentBlockMemberCachePoint); cp {
						i++
						results = append(results, list[i])
					}
				}
			} else {
				content = append(content, list[i])
			}
		}
	}
	return append(results, content...)
}

func convertUserContent(m provider.Message) ([]types.ContentBlock, error) {
	return convertUserContentPolicy(m, cachePolicy{})
}

func convertUserContentPolicy(m provider.Message, cache cachePolicy) ([]types.ContentBlock, error) {
	// toolResult blocks must precede other content in a user message.
	var results []types.ContentBlock
	var content []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			content = append(content, &types.ContentBlockMemberText{Value: text})
			if cache.marks(c) {
				content = append(content, &types.ContentBlockMemberCachePoint{Value: cache.point(c.CacheControl)})
			}
		}

		if c.File != nil {
			block, err := convertFile(c.File)

			if err != nil {
				return nil, err
			}

			content = append(content, block)
		}

		if c.ToolResult != nil {
			var blocks []types.ToolResultContentBlock

			for _, p := range c.ToolResult.Parts {
				if p.Text != "" {
					blocks = append(blocks, &types.ToolResultContentBlockMemberText{Value: p.Text})
				}

				if p.File != nil {
					block, err := convertToolResultFile(p.File)

					if err != nil {
						return nil, err
					}

					blocks = append(blocks, block)
				}
			}

			if len(blocks) == 0 {
				blocks = []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: "OK"},
				}
			}

			status := types.ToolResultStatusSuccess
			if c.ToolResult.IsError {
				status = types.ToolResultStatusError
			}

			results = append(results, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					Status: status,

					ToolUseId: aws.String(toolid.Sanitize(c.ToolResult.ID, 64)),

					Content: blocks,
				},
			})
			if cache.marks(c) {
				results = append(results, &types.ContentBlockMemberCachePoint{Value: cache.point(c.CacheControl)})
			}
		}
	}

	return append(results, content...), nil
}

// Blocks keep the order the model produced them in, so interleaved thinking
// replays unchanged.
func convertAssistantContent(m provider.Message) ([]types.ContentBlock, error) {
	var content []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			content = append(content, &types.ContentBlockMemberText{Value: text})
		}

		if c.Reasoning != nil && c.Reasoning.Signature != "" {
			if c.Reasoning.Redacted {
				data, err := base64.StdEncoding.DecodeString(c.Reasoning.Signature)

				if err != nil {
					return nil, err
				}

				content = append(content, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberRedactedContent{
						Value: data,
					},
				})
			} else {
				// The Responses API presents signed thinking text as a summary.
				// Restore it exactly, as the native Anthropic adapter does.
				text := c.Reasoning.Text()
				if text == "" {
					text = c.Reasoning.SummaryText()
				}
				content = append(content, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberReasoningText{
						Value: types.ReasoningTextBlock{
							Text:      aws.String(text),
							Signature: aws.String(c.Reasoning.Signature),
						},
					},
				})
			}
		}

		if c.ToolCall != nil {
			arguments := c.ToolCall.Arguments

			if c.ToolCall.Kind == provider.ToolKindCustom && !custom.IsWrapped(arguments) {
				// freeform input from the client — re-encode it the way the
				// emulated tool was declared, or the replay would drop it
				arguments = custom.Wrap(arguments)
			}

			var data map[string]any

			decoder := json.NewDecoder(strings.NewReader(arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&data); err != nil || data == nil || decoder.Decode(new(any)) != io.EOF {
				data = map[string]any{}
			}

			content = append(content, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String(toolid.Sanitize(c.ToolCall.ID, 64)),

					Name:  aws.String(provider.FlattenToolName(*c.ToolCall)),
					Input: newDocument(data),
				},
			})
		}
	}

	return content, nil
}

func (c *Completer) convertToolConfig(tools []provider.Tool, options *provider.ToolOptions) (*types.ToolConfiguration, error) {
	return c.convertToolConfigPolicy(tools, options, cachePolicy{})
}

func (c *Completer) convertToolConfigPolicy(tools []provider.Tool, options *provider.ToolOptions, cache cachePolicy) (*types.ToolConfiguration, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	result := &types.ToolConfiguration{}

	for _, t := range tools {
		if t.Kind == provider.ToolKindTextEditor {
			t = texteditor.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindComputer {
			t = computeruse.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindShell {
			t = shell.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindCustom {
			// Converse only accepts JSON schemas — carry the freeform text in a
			// single string parameter and unwrap it at the stream boundary
			t = custom.FunctionTool(t)
		}

		if t.Kind != provider.ToolKindFunction {
			return nil, provider.UnsupportedToolError(t)
		}

		tool := types.ToolSpecification{
			Name: aws.String(t.Name),
		}

		if t.Description != "" {
			tool.Description = aws.String(t.Description)
		}

		params := t.Parameters

		// Only forward strict when explicitly enabled and the model supports it:
		// strict=false carries no information, and models without structured
		// output reject the field itself. Strict mode also rejects constraint
		// keywords other providers accept, so sanitize only when it is sent.
		if t.Strict != nil && *t.Strict && supportsStrictTools(c.model) {
			tool.Strict = t.Strict
			params = claude.SanitizeSchema(params)
		}

		if len(params) > 0 {
			tool.InputSchema = &types.ToolInputSchemaMemberJson{
				Value: newDocument(params),
			}
		}

		result.Tools = append(result.Tools, &types.ToolMemberToolSpec{Value: tool})
	}

	// Add cache point after tool definitions for Claude models
	if isClaudeModel(c.model) && !cache.explicit {
		result.Tools = append(result.Tools, &types.ToolMemberCachePoint{
			Value: cache.point(nil),
		})
	}

	if options != nil {
		switch options.Choice {
		case provider.ToolChoiceNone:
			return nil, nil

		case provider.ToolChoiceAuto:
			result.ToolChoice = &types.ToolChoiceMemberAuto{
				Value: types.AutoToolChoice{},
			}

		case provider.ToolChoiceAny:
			if claude.MatchesModel(c.model, claude.NoForcedToolChoiceModels) {
				return nil, &provider.ProviderError{
					Code:    400,
					Type:    "invalid_request_error",
					Message: fmt.Sprintf("bedrock: model %s does not support forced tool_choice; use auto or none", c.model),
				}
			}

			if len(options.Allowed) == 1 {
				result.ToolChoice = &types.ToolChoiceMemberTool{
					Value: types.SpecificToolChoice{
						Name: aws.String(options.Allowed[0]),
					},
				}
			} else {
				result.ToolChoice = &types.ToolChoiceMemberAny{
					Value: types.AnyToolChoice{},
				}
			}
		}
	}

	return result, nil
}

func inputHasToolBlocks(messages []provider.Message) bool {
	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil || c.ToolResult != nil {
				return true
			}
		}
	}
	return false
}

// withHistoryTools ensures toolConfig defines any tool referenced by historical
// toolUse blocks even when the caller omits or narrows tools on a later turn.
func withHistoryTools(tools []provider.Tool, messages []provider.Message) []provider.Tool {
	known := make(map[string]bool, len(tools))
	for _, t := range tools {
		switch t.Kind {
		case provider.ToolKindTextEditor:
			known[texteditor.FunctionTool(t).Name] = true
		case provider.ToolKindComputer:
			known[computeruse.FunctionTool(t).Name] = true
		case provider.ToolKindShell:
			known[shell.FunctionTool(t).Name] = true
		default:
			if t.Name != "" {
				known[t.Name] = true
			}
		}
	}

	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall == nil {
				continue
			}
			name := provider.FlattenToolName(*c.ToolCall)
			if name == "" || known[name] {
				continue
			}
			known[name] = true
			tools = append(tools, provider.Tool{
				Name:       name,
				Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			})
		}
	}

	return tools
}

func convertToolResultFile(val *provider.File) (types.ToolResultContentBlock, error) {
	if format, ok := convertDocumentFormat(val.ContentType); ok {
		return &types.ToolResultContentBlockMemberDocument{
			Value: types.DocumentBlock{
				Name:   aws.String(uuid.NewString()),
				Format: format,

				Source: &types.DocumentSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	if format, ok := convertImageFormat(val.ContentType); ok {
		return &types.ToolResultContentBlockMemberImage{
			Value: types.ImageBlock{
				Format: format,

				Source: &types.ImageSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	if format, ok := convertVideoFormat(val.ContentType); ok {
		return &types.ToolResultContentBlockMemberVideo{
			Value: types.VideoBlock{
				Format: format,

				Source: &types.VideoSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("unsupported content type: %s", val.ContentType)
}

func convertFile(val *provider.File) (types.ContentBlock, error) {
	if format, ok := convertDocumentFormat(val.ContentType); ok {
		return &types.ContentBlockMemberDocument{
			Value: types.DocumentBlock{
				Name:   aws.String(uuid.NewString()),
				Format: format,

				Source: &types.DocumentSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	if format, ok := convertImageFormat(val.ContentType); ok {
		return &types.ContentBlockMemberImage{
			Value: types.ImageBlock{
				Format: format,

				Source: &types.ImageSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	if format, ok := convertVideoFormat(val.ContentType); ok {
		return &types.ContentBlockMemberVideo{
			Value: types.VideoBlock{
				Format: format,

				Source: &types.VideoSourceMemberBytes{
					Value: val.Content,
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("unsupported content type: %s", val.ContentType)
}

var documentFormats = map[string]types.DocumentFormat{
	"application/pdf":    types.DocumentFormatPdf,
	"application/msword": types.DocumentFormatDoc,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": types.DocumentFormatDocx,
	"application/vnd.ms-excel": types.DocumentFormatXls,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": types.DocumentFormatXlsx,
	"text/html":     types.DocumentFormatHtml,
	"text/plain":    types.DocumentFormatTxt,
	"text/csv":      types.DocumentFormatCsv,
	"text/markdown": types.DocumentFormatMd,
}

var imageFormats = map[string]types.ImageFormat{
	"image/png":  types.ImageFormatPng,
	"image/jpeg": types.ImageFormatJpeg,
	"image/gif":  types.ImageFormatGif,
	"image/webp": types.ImageFormatWebp,
}

var videoFormats = map[string]types.VideoFormat{
	"video/matroska":  types.VideoFormatMkv,
	"video/quicktime": types.VideoFormatMov,
	"video/mp4":       types.VideoFormatMp4,
	"video/webm":      types.VideoFormatWebm,
	"video/x-flv":     types.VideoFormatFlv,
	"video/mpeg":      types.VideoFormatMpeg,
	"video/x-ms-wmv":  types.VideoFormatWmv,
	"video/3gpp":      types.VideoFormatThreeGp,
}

func convertDocumentFormat(mime string) (types.DocumentFormat, bool) {
	format, ok := documentFormats[mime]
	return format, ok
}

func convertImageFormat(mime string) (types.ImageFormat, bool) {
	format, ok := imageFormats[mime]
	return format, ok
}

func convertVideoFormat(mime string) (types.VideoFormat, bool) {
	format, ok := videoFormats[mime]
	return format, ok
}

func toUsage(val *types.TokenUsage) *provider.Usage {
	if val == nil {
		return nil
	}

	inputTokens := int(aws.ToInt32(val.InputTokens))
	outputTokens := int(aws.ToInt32(val.OutputTokens))

	cacheReadInputTokens := int(aws.ToInt32(val.CacheReadInputTokens))
	cacheWriteInputTokens := int(aws.ToInt32(val.CacheWriteInputTokens))

	// Normalize InputTokens to a cache-inclusive total (like OpenAI/Gemini do).
	// Bedrock reports InputTokens as only new/non-cached tokens, with cache
	// read/write tokens counted separately; fold them in so the intermediate
	// Usage has one consistent meaning across providers.
	totalInputTokens := inputTokens + cacheReadInputTokens + cacheWriteInputTokens

	result := &provider.Usage{
		InputTokens:  totalInputTokens,
		OutputTokens: outputTokens,

		CacheReadInputTokens:     cacheReadInputTokens,
		CacheCreationInputTokens: cacheWriteInputTokens,
	}

	if len(val.CacheDetails) > 0 {
		details := &provider.CacheWriteTokensDetails{}
		for _, entry := range val.CacheDetails {
			switch entry.Ttl {
			case types.CacheTTLFiveMinutes:
				details.StandardTokens += int(aws.ToInt32(entry.InputTokens))
			case types.CacheTTLOneHour:
				details.ExtendedTokens += int(aws.ToInt32(entry.InputTokens))
			}
		}
		result.MergeCacheWrites(cacheWriteInputTokens, details)
	}
	return result
}
