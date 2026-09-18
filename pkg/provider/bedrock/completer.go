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
	"github.com/adrianliechti/wingman/pkg/provider/toolid"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"
	"github.com/adrianliechti/wingman/pkg/provider/tools/toolsearch"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
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

	// Configure adaptive retry mode for throttle-based rate limiting
	// Keep attempts low to reduce the risk of duplicate billing on retries for streaming requests

	// configOptions = append(configOptions, config.WithRetryer(func() aws.Retryer {
	// 	return retry.NewAdaptiveMode(func(o *retry.AdaptiveModeOptions) {
	// 		o.StandardOptions = append(o.StandardOptions, func(so *retry.StandardOptions) {
	// 			so.MaxAttempts = 3
	// 			so.MaxBackoff = 20 * time.Second
	// 		})
	// 	})
	// }))

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
			yield(nil, err)
			return
		}

		params := &bedrockruntime.ConverseStreamInput{
			ModelId: req.ModelId,

			Messages: req.Messages,

			System:     req.System,
			ToolConfig: req.ToolConfig,

			InferenceConfig: req.InferenceConfig,
			OutputConfig:    req.OutputConfig,

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

		// Schema mode emulates structured output with a forced tool; its
		// blocks stream as text while every other tool call stays a call.
		schemaBlocks := map[int32]bool{}
		sawToolCall := false

		// Emulated custom tools stream JSON-wrapped arguments. Their fragments
		// cannot be unwrapped one at a time, so buffer them per block and emit
		// the freeform text once the block closes.
		customArgs := map[int32]*strings.Builder{}

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
									provider.ReasoningContent(provider.Reasoning{
										Text: r.Value,
									}),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}

					case *types.ReasoningContentBlockDeltaMemberSignature:
						delta := &provider.Completion{
							ID:    id,
							Model: c.model,

							Message: &provider.Message{
								Role: provider.MessageRoleAssistant,

								Content: []provider.Content{
									provider.ReasoningContent(provider.Reasoning{
										Signature: r.Value,
									}),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}

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
								provider.TextContent(b.Value),
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
							provider.TextContent(aws.ToString(b.Value.Input)),
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
// fields is accepted. The resolved thinking configuration is returned too,
// since temperature must be cleared while thinking is enabled.
func (c *Completer) converseAdditionalFields(messages []provider.Message, options *provider.CompleteOptions) (map[string]any, thinking) {
	// Forced tool calls (emulated schema mode, tool choice "any") are
	// incompatible with thinking on Anthropic models over Bedrock.
	forced := c.schemaAsTool(options) ||
		(options.ToolOptions != nil && options.ToolOptions.Choice == provider.ToolChoiceAny)

	thinking := c.resolveThinking(messages, options, forced)

	fields := map[string]any{}

	if thinking.Enabled {
		config := map[string]any{"type": "adaptive"}
		if !thinking.Summarized {
			config["display"] = "omitted"
		}

		fields["thinking"] = config
	} else if thinking.Disabled && matchesModel(c.model, DefaultThinkingModels) {
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
	return (options.Schema.Strict == nil || !*options.Schema.Strict) && schemaAllowsAdditionalProperties(options.Schema.Properties)
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
		if mode != "" && mode != provider.ReasoningContextAuto {
			return nil, &provider.ProviderError{
				Code:    400,
				Type:    "invalid_request_error",
				Message: "Bedrock Converse does not support explicit reasoning.context; omit it or use auto",
			}
		}
	}
	input, options = c.resolveInput(input, options)

	midSystem := c.supportsMidSystem()

	messages, err := c.convertMessages(input, midSystem)

	if err != nil {
		return nil, err
	}

	// ToolChoiceNone suppresses toolConfig, but Bedrock requires it when message history
	// contains toolUse/toolResult blocks. In that case, fall back to no ToolChoice (auto).
	toolOptions := options.ToolOptions

	if toolOptions != nil && toolOptions.Choice == provider.ToolChoiceNone && inputHasToolBlocks(input) {
		toolOptions = nil
	}

	config, err := c.convertToolConfig(provider.FlattenTools(options.Tools), toolOptions)

	if err != nil {
		return nil, err
	}

	var output *types.OutputConfig

	// Schema mode: models with structured outputs take the schema natively
	// as outputConfig.textFormat; the grammar is enforced, so the schema must
	// meet the strict-mode subset like a strict tool does. Everything else
	// exposes the schema as a tool and forces its use.
	if options.Schema != nil && !c.schemaAsTool(options) {
		schema := ensureAdditionalPropertiesFalse(sanitizeStrictSchema(options.Schema.Properties))

		data, err := json.Marshal(schema)

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
			properties = ensureAdditionalPropertiesFalse(sanitizeStrictSchema(properties))
		}

		tool.InputSchema = &types.ToolInputSchemaMemberJson{
			Value: document.NewLazyDocument(properties),
		}

		config.Tools = append(config.Tools, &types.ToolMemberToolSpec{Value: tool})

		if len(config.Tools) > 1 {
			// Client tools stay callable: the model must call some tool, and
			// the schema tool is the only way to produce the final answer.
			config.ToolChoice = &types.ToolChoiceMemberAny{}
		} else {
			config.ToolChoice = &types.ToolChoiceMemberTool{
				Value: types.SpecificToolChoice{
					Name: aws.String(options.Schema.Name),
				},
			}
		}
	}

	inference := &types.InferenceConfiguration{}

	if options.MaxTokens != nil {
		inference.MaxTokens = aws.Int32(int32(*options.MaxTokens))
	}

	if options.Temperature != nil && !matchesModel(c.model, NoSamplingModels) {
		inference.Temperature = options.Temperature
	}

	if len(options.Stop) > 0 {
		inference.StopSequences = options.Stop
	}

	req := &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.model),

		Messages: messages,

		System:     c.convertSystem(input, midSystem),
		ToolConfig: config,

		InferenceConfig: inference,
		OutputConfig:    output,
	}

	fields, thinking := c.converseAdditionalFields(input, options)

	if thinking.Enabled {
		inference.Temperature = nil
	}

	if len(fields) > 0 {
		req.AdditionalModelRequestFields = document.NewLazyDocument(fields)
	}

	return req, nil
}

// convertSystem collects the top-level system prompt: every system message
// when the model takes none inside the conversation, otherwise only those
// ahead of the first turn — later ones stay in place (see convertMessages).
func (c *Completer) convertSystem(messages []provider.Message, midSystem bool) []types.SystemContentBlock {
	var result []types.SystemContentBlock

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			if midSystem {
				break
			}

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
		}
	}

	if len(result) == 0 {
		return nil
	}

	// Add cache point after system messages for Claude models
	if isClaudeModel(c.model) {
		result = append(result, &types.SystemContentBlockMemberCachePoint{
			Value: types.CachePointBlock{
				Type: types.CachePointTypeDefault,
			},
		})
	}

	return result
}

// convertMessages builds the conversation. System messages ahead of the
// first turn always belong to the top-level system prompt. Later ones are
// kept in place as role "system" messages on models that accept them; other
// models had their text hoisted by convertSystem.
func (c *Completer) convertMessages(messages []provider.Message, midSystem bool) ([]types.Message, error) {
	var result []types.Message

	// Pre-process: merge consecutive messages with the same role (required by Bedrock API)
	var merged []provider.Message
	for _, m := range messages {
		if len(merged) > 0 && merged[len(merged)-1].Role == m.Role {
			last := &merged[len(merged)-1]
			last.Content = append(last.Content, m.Content...)
		} else {
			merged = append(merged, m)
		}
	}

	started := false

	for _, m := range merged {
		if m.Role == provider.MessageRoleSystem {
			if !midSystem || !started {
				continue
			}
		} else {
			started = true
		}

		var err error

		var role types.ConversationRole
		var content []types.ContentBlock

		switch m.Role {
		case provider.MessageRoleSystem:
			role = types.ConversationRoleSystem
			content = convertSystemContent(m)

		case provider.MessageRoleUser:
			role = types.ConversationRoleUser
			content, err = convertUserContent(m)

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

		result = append(result, types.Message{
			Role:    role,
			Content: content,
		})
	}

	// Add cache point to the last user message for Claude models
	if isClaudeModel(c.model) && len(result) > 0 {
		for i := len(result) - 1; i >= 0; i-- {
			if result[i].Role == types.ConversationRoleUser {
				result[i].Content = append(result[i].Content, &types.ContentBlockMemberCachePoint{
					Value: types.CachePointBlock{
						Type: types.CachePointTypeDefault,
					},
				})
				break
			}
		}
	}

	return result, nil
}

// convertSystemContent builds a mid-conversation system message from the
// resolved instructions.
func convertSystemContent(m provider.Message) []types.ContentBlock {
	var content []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			content = append(content, &types.ContentBlockMemberText{Value: text})
		}
	}

	return content
}

func convertUserContent(m provider.Message) ([]types.ContentBlock, error) {
	var content []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			content = append(content, &types.ContentBlockMemberText{Value: text})
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

			content = append(content, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					Status: status,

					ToolUseId: aws.String(toolid.Sanitize(c.ToolResult.ID, 64)),

					Content: blocks,
				},
			})
		}
	}

	return content, nil
}

// Bedrock rejects assistant turns where a text block sits between a toolUse
// block and its toolResult, so blocks are grouped reasoning -> text -> toolUse
// (stable within each group).
func convertAssistantContent(m provider.Message) ([]types.ContentBlock, error) {
	var reasoning, texts, calls []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			texts = append(texts, &types.ContentBlockMemberText{Value: text})
		}

		if c.Reasoning != nil && c.Reasoning.Signature != "" {
			if c.Reasoning.Redacted {
				data, err := base64.StdEncoding.DecodeString(c.Reasoning.Signature)

				if err != nil {
					return nil, err
				}

				reasoning = append(reasoning, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberRedactedContent{
						Value: data,
					},
				})
			} else {
				// The Responses API presents signed thinking text as a summary.
				// Restore it exactly, as the native Anthropic adapter does.
				text := c.Reasoning.Text
				if text == "" {
					text = c.Reasoning.Summary
				}
				reasoning = append(reasoning, &types.ContentBlockMemberReasoningContent{
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

			if err := json.Unmarshal([]byte(arguments), &data); err != nil || data == nil {
				data = map[string]any{}
			}

			calls = append(calls, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String(toolid.Sanitize(c.ToolCall.ID, 64)),

					Name:  aws.String(provider.FlattenToolName(*c.ToolCall)),
					Input: document.NewLazyDocument(data),
				},
			})
		}
	}

	content := make([]types.ContentBlock, 0, len(reasoning)+len(texts)+len(calls))
	content = append(content, reasoning...)
	content = append(content, texts...)
	content = append(content, calls...)

	return content, nil
}

func (c *Completer) convertToolConfig(tools []provider.Tool, options *provider.ToolOptions) (*types.ToolConfiguration, error) {
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
			params = sanitizeStrictSchema(params)
		}

		if len(params) > 0 {
			tool.InputSchema = &types.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(params),
			}
		}

		result.Tools = append(result.Tools, &types.ToolMemberToolSpec{Value: tool})
	}

	// Add cache point after tool definitions for Claude models
	if isClaudeModel(c.model) {
		result.Tools = append(result.Tools, &types.ToolMemberCachePoint{
			Value: types.CachePointBlock{
				Type: types.CachePointTypeDefault,
			},
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

	return &provider.Usage{
		InputTokens:  totalInputTokens,
		OutputTokens: outputTokens,

		CacheReadInputTokens:     cacheReadInputTokens,
		CacheCreationInputTokens: cacheWriteInputTokens,
	}
}
