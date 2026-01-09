package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"reflect"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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

	// Configure retry with exponential backoff for throttling errors
	configOptions = append(configOptions, config.WithRetryer(func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 10
			o.MaxBackoff = 60 * time.Second
		})
	}))

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

		resp, err := c.client.ConverseStream(ctx, params)

		if err != nil {
			yield(nil, err)
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
			yield(nil, err)
			return
		}
	}
}

func (c *Completer) convertConverseInput(input []provider.Message, options *provider.CompleteOptions) (*bedrockruntime.ConverseInput, error) {
	messages, err := convertMessages(input)

	if err != nil {
		return nil, err
	}

	toolConfig := convertToolConfig(options.Tools)

	// Schema mode: create a tool with the schema and force ToolChoice
	if options.Schema != nil {
		if toolConfig == nil {
			toolConfig = &types.ToolConfiguration{}
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

		toolConfig.Tools = append(toolConfig.Tools, &types.ToolMemberToolSpec{Value: tool})
		toolConfig.ToolChoice = &types.ToolChoiceMemberTool{
			Value: types.SpecificToolChoice{
				Name: aws.String(options.Schema.Name),
			},
		}
	}

	return &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.model),

		Messages: messages,

		System:     convertSystem(input),
		ToolConfig: toolConfig,
	}, nil
}

func convertSystem(messages []provider.Message) []types.SystemContentBlock {
	var result []types.SystemContentBlock

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			continue
		}

		for _, c := range m.Content {
			if c.Text == "" {
				continue
			}

			system := &types.SystemContentBlockMemberText{
				Value: c.Text,
			}

			result = append(result, system)
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

func convertMessages(messages []provider.Message) ([]types.Message, error) {
	var result []types.Message

	for _, m := range messages {
		switch m.Role {

		case provider.MessageRoleSystem:
			continue

		case provider.MessageRoleUser:
			message := types.Message{
				Role: types.ConversationRoleUser,
			}

			for _, c := range m.Content {
				if c.Text != "" {
					block := &types.ContentBlockMemberText{
						Value: c.Text,
					}

					message.Content = append(message.Content, block)
				}

				if c.File != nil {
					block, err := convertFile(c.File)

					if err != nil {
						return nil, err
					}

					message.Content = append(message.Content, block)
				}

				if c.ToolResult != nil {
					var data any
					json.Unmarshal([]byte(c.ToolResult.Data), &data)

					if reflect.TypeOf(data).Kind() != reflect.Map {
						data = map[string]any{
							"result": data,
						}
					}

					block := &types.ContentBlockMemberToolResult{
						Value: types.ToolResultBlock{
							ToolUseId: aws.String(c.ToolResult.ID),

							Content: []types.ToolResultContentBlock{
								&types.ToolResultContentBlockMemberJson{
									Value: document.NewLazyDocument(data),
								},
							},
						},
					}

					message.Content = append(message.Content, block)
				}
			}

			result = append(result, message)

		case provider.MessageRoleAssistant:
			message := types.Message{
				Role: types.ConversationRoleAssistant,
			}

			for _, c := range m.Content {
				if c.Text != "" {
					content := &types.ContentBlockMemberText{
						Value: c.Text,
					}

					message.Content = append(message.Content, content)
				}

				if c.ToolCall != nil {
					var data map[string]any
					if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &data); err != nil || data == nil {
						data = map[string]any{}
					}

					content := &types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String(c.ToolCall.ID),
							Name:      aws.String(c.ToolCall.Name),

							Input: document.NewLazyDocument(data),
						},
					}

					message.Content = append(message.Content, content)
				}
			}

			result = append(result, message)

		default:
			return nil, errors.New("unsupported message role")
		}
	}

	return result, nil
}

func convertToolConfig(tools []provider.Tool) *types.ToolConfiguration {
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

	return result
}

func convertFile(val *provider.File) (types.ContentBlock, error) {
	if val == nil {
		return nil, nil
	}

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

func convertDocumentFormat(mime string) (types.DocumentFormat, bool) {
	switch mime {
	case "application/pdf":
		return types.DocumentFormatPdf, true

	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return types.DocumentFormatDocx, true

	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return types.DocumentFormatXlsx, true

	case "text/plain":
		return types.DocumentFormatTxt, true

	case "text/csv":
		return types.DocumentFormatCsv, true

	case "text/markdown":
		return types.DocumentFormatMd, true
	}

	return "", false
}

func convertImageFormat(mime string) (types.ImageFormat, bool) {
	switch mime {
	case "image/png":
		return types.ImageFormatPng, true

	case "image/jpeg":
		return types.ImageFormatJpeg, true

	case "image/gif":
		return types.ImageFormatGif, true

	case "image/webp":
		return types.ImageFormatWebp, true
	}

	return "", false
}

func convertVideoFormat(mime string) (types.VideoFormat, bool) {
	switch mime {
	case "video/matroska":
		return types.VideoFormatMkv, true

	case "video/quicktime":
		return types.VideoFormatMov, true

	case "video/mp4":
		return types.VideoFormatMp4, true

	case "video/webm":
		return types.VideoFormatWebm, true
	}

	return "", false
}

func toUsage(val *types.TokenUsage) *provider.Usage {
	if val == nil {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(aws.ToInt32(val.InputTokens)),
		OutputTokens: int(aws.ToInt32(val.OutputTokens)),
	}
}
