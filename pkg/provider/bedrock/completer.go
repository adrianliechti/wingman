package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
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
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	config, err := config.LoadDefaultConfig(context.Background())

	if err != nil {
		return nil, err
	}

	client := bedrockruntime.NewFromConfig(config)

	return &Completer{
		Config: cfg,

		client: client,
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := c.convertConverseInput(messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		req := &bedrockruntime.ConverseStreamInput{
			ModelId: req.ModelId,

			Messages: req.Messages,

			System:     req.System,
			ToolConfig: req.ToolConfig,
		}

		return c.completeStream(ctx, req, options)
	}

	return c.complete(ctx, req, options)
}

func (c *Completer) complete(ctx context.Context, req *bedrockruntime.ConverseInput, options *provider.CompleteOptions) (*provider.Completion, error) {
	resp, err := c.client.Converse(ctx, req)

	if err != nil {
		return nil, err
	}

	return &provider.Completion{
		ID:     uuid.New().String(),
		Reason: toCompletionResult(resp.StopReason),

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,

			Content: toContent(resp.Output),
		},

		Usage: toUsage(resp.Usage),
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req *bedrockruntime.ConverseStreamInput, options *provider.CompleteOptions) (*provider.Completion, error) {
	resp, err := c.client.ConverseStream(ctx, req)

	if err != nil {
		return nil, err
	}

	id := uuid.NewString()

	result := provider.CompletionAccumulator{}

	for event := range resp.GetStream().Events() {
		switch v := event.(type) {
		case *types.ConverseStreamOutputMemberMessageStart:
			delta := provider.Completion{
				ID: id,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			result.Add(delta)

		case *types.ConverseStreamOutputMemberContentBlockStart:
			switch b := v.Value.Start.(type) {
			case *types.ContentBlockStartMemberToolUse:
				delta := provider.Completion{
					ID: id,

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

				result.Add(delta)

			default:
				fmt.Printf("unknown block type, %T\n", b)
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch b := v.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				delta := provider.Completion{
					ID: id,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(b.Value),
						},
					},
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}

			case *types.ContentBlockDeltaMemberToolUse:
				delta := provider.Completion{
					ID: id,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								Arguments: *b.Value.Input,
							}),
						},
					},
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}

			default:
				fmt.Printf("unknown block type, %T\n", b)
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:

		case *types.ConverseStreamOutputMemberMessageStop:
			delta := provider.Completion{
				ID: id,

				Reason: toCompletionResult(v.Value.StopReason),

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.TextContent(""),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}

		case *types.ConverseStreamOutputMemberMetadata:
			delta := provider.Completion{
				ID: id,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.TextContent(""),
					},
				},

				Usage: toUsage(v.Value.Usage),
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}

		case *types.UnknownUnionMember:
			fmt.Println("unknown tag", v.Tag)

		default:
			fmt.Printf("unknown event type, %T\n", v)
		}
	}

	return result.Result(), nil
}

func (c *Completer) convertConverseInput(input []provider.Message, options *provider.CompleteOptions) (*bedrockruntime.ConverseInput, error) {
	messages, err := convertMessages(input)

	if err != nil {
		return nil, err
	}

	return &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.model),

		Messages: messages,

		System:     convertSystem(input),
		ToolConfig: convertToolConfig(options.Tools),
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
					var data any
					json.Unmarshal([]byte(c.ToolCall.Arguments), &data)

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

	data, err := io.ReadAll(val.Content)

	if err != nil {
		return nil, err
	}

	if format, ok := convertDocumentFormat(val.ContentType); ok {
		return &types.ContentBlockMemberDocument{
			Value: types.DocumentBlock{
				Name:   aws.String(uuid.NewString()),
				Format: format,
				Source: &types.DocumentSourceMemberBytes{
					Value: data,
				},
			},
		}, nil
	}

	if format, ok := convertImageFormat(val.ContentType); ok {
		return &types.ContentBlockMemberImage{
			Value: types.ImageBlock{
				Format: format,
				Source: &types.ImageSourceMemberBytes{
					Value: data,
				},
			},
		}, nil
	}

	if format, ok := convertVideoFormat(val.ContentType); ok {
		return &types.ContentBlockMemberVideo{
			Value: types.VideoBlock{
				Format: format,
				Source: &types.VideoSourceMemberBytes{
					Value: data,
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

func toCompletionResult(val types.StopReason) provider.CompletionReason {
	switch val {
	case types.StopReasonEndTurn:
		return provider.CompletionReasonStop

	case types.StopReasonToolUse:
		return provider.CompletionReasonTool

	case types.StopReasonMaxTokens:
		return provider.CompletionReasonLength

	case types.StopReasonStopSequence:
		return provider.CompletionReasonStop

	case types.StopReasonGuardrailIntervened:
		return provider.CompletionReasonFilter

	case types.StopReasonContentFiltered:
		return provider.CompletionReasonFilter

	default:
		return ""
	}
}

func toRole(val types.ConversationRole) provider.MessageRole {
	switch val {
	case types.ConversationRoleUser:
		return provider.MessageRoleUser

	case types.ConversationRoleAssistant:
		return provider.MessageRoleAssistant

	default:
		return ""
	}
}

func toContent(val types.ConverseOutput) []provider.Content {
	message, ok := val.(*types.ConverseOutputMemberMessage)

	if !ok {
		return nil
	}

	var parts []provider.Content

	for _, b := range message.Value.Content {
		switch block := b.(type) {
		case *types.ContentBlockMemberText:
			parts = append(parts, provider.TextContent(block.Value))

		case *types.ContentBlockMemberToolUse:
			data, _ := block.Value.Input.MarshalSmithyDocument()

			tool := provider.ToolCall{
				ID:   aws.ToString(block.Value.ToolUseId),
				Name: aws.ToString(block.Value.Name),

				Arguments: string(data),
			}

			parts = append(parts, provider.ToolCallContent(tool))
		}
	}

	return parts
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
