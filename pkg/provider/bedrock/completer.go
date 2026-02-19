package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config

	client *bedrockruntime.Client
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model:  model,
		client: http.DefaultClient,
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
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		req, err := c.convertConverseInput(messages, options)

		if err != nil {
			yield(nil, err)
			return
		}

		config := &types.InferenceConfiguration{}

		if options.MaxTokens != nil {
			config.MaxTokens = aws.Int32(int32(*options.MaxTokens))
		}

		if options.Temperature != nil {
			config.Temperature = options.Temperature
		}

		if len(options.Stop) > 0 {
			config.StopSequences = options.Stop
		}

		params := &bedrockruntime.ConverseStreamInput{
			ModelId: req.ModelId,

			Messages: req.Messages,

			System:     req.System,
			ToolConfig: req.ToolConfig,

			InferenceConfig: config,
		}

		// var budgetTokens int
		// switch options.Effort {
		// case provider.EffortMinimal:
		// 	budgetTokens = 1024
		// case provider.EffortLow:
		// 	budgetTokens = 2048
		// case provider.EffortMedium:
		// 	budgetTokens = 8192
		// case provider.EffortHigh:
		// 	budgetTokens = 32000
		// }

		// if budgetTokens > 0 {
		// 	params.AdditionalModelRequestFields = document.NewLazyDocument(map[string]any{
		// 		"thinking": map[string]any{
		// 			"type":          "enabled",
		// 			"budget_tokens": budgetTokens,
		// 		},
		// 	})
		// }

		resp, err := c.client.ConverseStream(ctx, params)

		if err != nil {
			yield(nil, convertError(err))
			return
		}

		id := uuid.NewString()

		for event := range resp.GetStream().Events() {
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
					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:   aws.ToString(b.Value.ToolUseId),
									Name: aws.ToString(b.Value.Name),
								}),
							},
						},
					}

					// Schema mode: convert tool call to text content
					if options.Schema != nil {
						delta.Message.Content = []provider.Content{
							provider.TextContent(""),
						}
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
					delta := &provider.Completion{
						ID:    id,
						Model: c.model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									Arguments: *b.Value.Input,
								}),
							},
						},
					}

					// Schema mode: convert tool arguments to text content
					if options.Schema != nil {
						delta.Message.Content = []provider.Content{
							provider.TextContent(*b.Value.Input),
						}
					}

					if !yield(delta, nil) {
						return
					}

				default:
					// Unknown delta type, skip silently
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:

			case *types.ConverseStreamOutputMemberMessageStop:
				delta := &provider.Completion{
					ID:    id,
					Model: c.model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(""),
						},
					},
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

		// Check for stream errors
		if err := resp.GetStream().Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
	}
}

// convertError extracts meaningful error information from AWS SDK errors
func convertError(err error) error {
	if err == nil {
		return nil
	}

	// Handle Bedrock-specific error types
	var throttle *types.ThrottlingException
	if errors.As(err, &throttle) {
		return fmt.Errorf("bedrock throttling: %s", aws.ToString(throttle.Message))
	}

	var validation *types.ValidationException
	if errors.As(err, &validation) {
		return fmt.Errorf("bedrock validation: %s", aws.ToString(validation.Message))
	}

	var modelErr *types.ModelStreamErrorException
	if errors.As(err, &modelErr) {
		return fmt.Errorf("bedrock stream error: %s", aws.ToString(modelErr.Message))
	}

	var modelNotReady *types.ModelNotReadyException
	if errors.As(err, &modelNotReady) {
		return fmt.Errorf("bedrock model not ready: %s", aws.ToString(modelNotReady.Message))
	}

	var serviceUnavailable *types.ServiceUnavailableException
	if errors.As(err, &serviceUnavailable) {
		return fmt.Errorf("bedrock service unavailable: %s", aws.ToString(serviceUnavailable.Message))
	}

	var internalServer *types.InternalServerException
	if errors.As(err, &internalServer) {
		return fmt.Errorf("bedrock internal error: %s", aws.ToString(internalServer.Message))
	}

	var accessDenied *types.AccessDeniedException
	if errors.As(err, &accessDenied) {
		return fmt.Errorf("bedrock access denied: %s", aws.ToString(accessDenied.Message))
	}

	var modelTimeout *types.ModelTimeoutException
	if errors.As(err, &modelTimeout) {
		return fmt.Errorf("bedrock model timeout: %s", aws.ToString(modelTimeout.Message))
	}

	var modelError *types.ModelErrorException
	if errors.As(err, &modelError) {
		return fmt.Errorf("bedrock model error: %s", aws.ToString(modelError.Message))
	}

	var conflict *types.ConflictException
	if errors.As(err, &conflict) {
		return fmt.Errorf("bedrock conflict: %s", aws.ToString(conflict.Message))
	}

	var resourceNotFound *types.ResourceNotFoundException
	if errors.As(err, &resourceNotFound) {
		return fmt.Errorf("bedrock resource not found: %s", aws.ToString(resourceNotFound.Message))
	}

	var quotaExceeded *types.ServiceQuotaExceededException
	if errors.As(err, &quotaExceeded) {
		return fmt.Errorf("bedrock quota exceeded: %s", aws.ToString(quotaExceeded.Message))
	}

	// Extract AWS API error details (error code, message) for any other AWS errors
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return fmt.Errorf("bedrock error [%s]: %s", ae.ErrorCode(), ae.ErrorMessage())
	}

	return err
}

func (c *Completer) convertConverseInput(input []provider.Message, options *provider.CompleteOptions) (*bedrockruntime.ConverseInput, error) {
	messages, err := c.convertMessages(input)

	if err != nil {
		return nil, err
	}

	config := c.convertToolConfig(options.Tools, options.ToolOptions)

	// Schema mode: create a tool with the schema and force ToolChoice
	if options.Schema != nil {
		if config == nil {
			config = &types.ToolConfiguration{}
		}

		tool := types.ToolSpecification{
			Name: aws.String(options.Schema.Name),
		}

		if options.Schema.Description != "" {
			tool.Description = aws.String(options.Schema.Description)
		}

		if options.Schema.Schema != nil {
			tool.InputSchema = &types.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(options.Schema.Schema),
			}
		}

		config.Tools = append(config.Tools, &types.ToolMemberToolSpec{Value: tool})
		config.ToolChoice = &types.ToolChoiceMemberTool{
			Value: types.SpecificToolChoice{
				Name: aws.String(options.Schema.Name),
			},
		}
	}

	return &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.model),

		Messages: messages,

		System:     c.convertSystem(input),
		ToolConfig: config,
	}, nil
}

func (c *Completer) convertSystem(messages []provider.Message) []types.SystemContentBlock {
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

func (c *Completer) convertMessages(messages []provider.Message) ([]types.Message, error) {
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

	for _, m := range merged {
		if m.Role == provider.MessageRoleSystem {
			continue
		}

		var err error

		var role types.ConversationRole
		var content []types.ContentBlock

		switch m.Role {
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
			data := c.ToolResult.Data

			if data == "" {
				data = "OK"
			}

			content = append(content, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					Status: types.ToolResultStatusSuccess,

					ToolUseId: aws.String(c.ToolResult.ID),

					Content: []types.ToolResultContentBlock{
						&types.ToolResultContentBlockMemberText{Value: data},
					},
				},
			})
		}
	}

	return content, nil
}

func convertAssistantContent(m provider.Message) ([]types.ContentBlock, error) {
	var content []types.ContentBlock

	for _, c := range m.Content {
		if text := strings.TrimRight(c.Text, " \t\n\r"); text != "" {
			content = append(content, &types.ContentBlockMemberText{Value: text})
		}

		if c.Reasoning != nil {
			content = append(content, &types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberReasoningText{
					Value: types.ReasoningTextBlock{
						Text:      aws.String(c.Reasoning.Text),
						Signature: aws.String(c.Reasoning.Signature),
					},
				},
			})
		}

		if c.ToolCall != nil {
			var data map[string]any

			if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &data); err != nil || data == nil {
				data = map[string]any{}
			}

			content = append(content, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String(c.ToolCall.ID),

					Name:  aws.String(c.ToolCall.Name),
					Input: document.NewLazyDocument(data),
				},
			})
		}
	}

	return content, nil
}

func (c *Completer) convertToolConfig(tools []provider.Tool, options *provider.ToolOptions) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}

	result := &types.ToolConfiguration{}

	for _, t := range tools {
		tool := types.ToolSpecification{
			Name: aws.String(t.Name),
		}

		if t.Description != "" {
			tool.Description = aws.String(t.Description)
		}

		if len(t.Parameters) > 0 {
			tool.InputSchema = &types.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(t.Parameters),
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
			return nil

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

	return result
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

	return nil, errors.New("unsupported file format")
}

var documentFormats = map[string]types.DocumentFormat{
	"application/pdf": types.DocumentFormatPdf,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": types.DocumentFormatDocx,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       types.DocumentFormatXlsx,
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

	// Normalize InputTokens to include cached tokens (like OpenAI does)
	// Bedrock reports InputTokens as only new/non-cached tokens
	// OpenAI reports prompt_tokens as total (cached + non-cached)
	totalInputTokens := inputTokens + cacheReadInputTokens

	return &provider.Usage{
		InputTokens:  totalInputTokens,
		OutputTokens: outputTokens,

		CacheReadInputTokens:     cacheReadInputTokens,
		CacheCreationInputTokens: cacheWriteInputTokens,
	}
}
