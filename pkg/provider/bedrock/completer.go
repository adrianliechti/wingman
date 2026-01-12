package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"strings"
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

	return &provider.Usage{
		InputTokens:  int(aws.ToInt32(val.InputTokens)),
		OutputTokens: int(aws.ToInt32(val.OutputTokens)),
	}
}
