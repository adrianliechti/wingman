package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/adrianliechti/llama/pkg/provider"

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

	msgs, err := convertMessages(messages)

	if err != nil {
		return nil, err
	}

	if options.Stream == nil {
		resp, err := c.client.Converse(ctx, &bedrockruntime.ConverseInput{
			ModelId: aws.String(c.model),

			Messages: msgs,

			System:     convertSystem(messages),
			ToolConfig: convertToolConfig(options.Tools),
		})

		if err != nil {
			return nil, err
		}

		return &provider.Completion{
			ID:     uuid.New().String(),
			Reason: toCompletionResult(resp.StopReason),

			Message: provider.Message{
				Role: provider.MessageRoleAssistant,

				Content:   toContent(resp.Output),
				ToolCalls: toToolCalls(resp.Output),
			},
		}, nil
	} else {
		resp, err := c.client.ConverseStream(context.Background(), &bedrockruntime.ConverseStreamInput{
			ModelId: aws.String(c.model),

			Messages: msgs,

			System:     convertSystem(messages),
			ToolConfig: convertToolConfig(options.Tools),
		})

		if err != nil {
			return nil, err
		}

		result := &provider.Completion{
			ID: uuid.New().String(),

			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
			},

			//Usage: &provider.Usage{},
		}

		for event := range resp.GetStream().Events() {
			switch v := event.(type) {
			case *types.ConverseStreamOutputMemberMessageStart:
				result.Message.Role = toRole(v.Value.Role)

			case *types.ConverseStreamOutputMemberContentBlockStart:
				switch b := v.Value.Start.(type) {
				case *types.ContentBlockStartMemberToolUse:
					result.Message.ToolCalls = []provider.ToolCall{
						{
							ID:   aws.ToString(b.Value.ToolUseId),
							Name: aws.ToString(b.Value.Name),
						},
					}

				default:
					fmt.Printf("unknown block type, %T\n", b)
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				switch b := v.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					content := b.Value
					result.Message.Content += content

					if len(content) > 0 {
						completion := provider.Completion{
							ID: result.ID,

							Message: provider.Message{
								Role:    provider.MessageRoleAssistant,
								Content: content,
							},
						}

						if err := options.Stream(ctx, completion); err != nil {
							return nil, err
						}
					}

				case *types.ContentBlockDeltaMemberToolUse:
					content := *b.Value.Input

					if len(result.Message.ToolCalls) > 0 {
						index := len(result.Message.ToolCalls) - 1
						result.Message.ToolCalls[index].Arguments += content
					}

					if len(content) > 0 {
						completion := provider.Completion{
							ID: result.ID,

							Message: provider.Message{
								Role: provider.MessageRoleAssistant,

								ToolCalls: []provider.ToolCall{
									{
										Arguments: content,
									},
								},
							},
						}

						if err := options.Stream(ctx, completion); err != nil {
							return nil, err
						}
					}

				default:
					fmt.Printf("unknown block type, %T\n", b)
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:

			case *types.ConverseStreamOutputMemberMessageStop:
				reason := toCompletionResult(v.Value.StopReason)

				if reason != "" {
					result.Reason = reason
				}

			case *types.ConverseStreamOutputMemberMetadata:
				result.Usage = &provider.Usage{
					InputTokens:  int(aws.ToInt32(v.Value.Usage.InputTokens)),
					OutputTokens: int(aws.ToInt32(v.Value.Usage.OutputTokens)),
				}

			case *types.UnknownUnionMember:
				fmt.Println("unknown tag", v.Tag)

			default:
				fmt.Printf("unknown event type, %T\n", v)
			}
		}

		return result, nil
	}
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

			if m.Content != "" {
				content := &types.ContentBlockMemberText{
					Value: m.Content,
				}

				message.Content = append(message.Content, content)
			}

			for _, f := range m.Files {
				content, err := convertFile(f)

				if err != nil {
					return nil, err
				}

				message.Content = append(message.Content, content)
			}

			result = append(result, message)

		case provider.MessageRoleAssistant:
			message := types.Message{
				Role: types.ConversationRoleAssistant,
			}

			if m.Content != "" {
				content := &types.ContentBlockMemberText{
					Value: m.Content,
				}

				message.Content = append(message.Content, content)
			}

			for _, t := range m.ToolCalls {
				var data any
				json.Unmarshal([]byte(t.Arguments), &data)

				content := &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(t.ID),
						Name:      aws.String(t.Name),

						Input: document.NewLazyDocument(data),
					},
				}

				message.Content = append(message.Content, content)
			}

			result = append(result, message)

		case provider.MessageRoleTool:
			var data any
			json.Unmarshal([]byte(m.Content), &data)

			if reflect.TypeOf(data).Kind() == reflect.Slice {
				data = map[string]any{
					"result": data,
				}
			}

			result = append(result, types.Message{
				Role: types.ConversationRoleUser,

				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolResult{
						Value: types.ToolResultBlock{
							ToolUseId: aws.String(m.Tool),

							Content: []types.ToolResultContentBlock{
								&types.ToolResultContentBlockMemberJson{
									Value: document.NewLazyDocument(data),
								},
							},
						},
					},
				},
			})

		default:
			return nil, errors.New("unsupported message role")
		}
	}

	return result, nil
}

func convertSystem(messages []provider.Message) []types.SystemContentBlock {
	var result []types.SystemContentBlock

	for _, m := range messages {
		if m.Role != provider.MessageRoleSystem {
			continue
		}

		system := &types.SystemContentBlockMemberText{
			Value: m.Content,
		}

		result = append(result, system)
	}

	if len(result) == 0 {
		return nil
	}

	return result
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

func convertFile(val provider.File) (types.ContentBlock, error) {
	format := strings.TrimPrefix(path.Ext(val.Name), ".")

	if format == "jpg" || format == "jpe" {
		format = "jpeg"
	}

	if format == "m4v" {
		format = "mp4"
	}

	data, err := io.ReadAll(val.Content)

	if err != nil {
		return nil, err
	}

	if slices.Contains(types.ImageFormat("").Values(), types.ImageFormat(format)) {
		return &types.ContentBlockMemberImage{
			Value: types.ImageBlock{
				Format: types.ImageFormat(format),
				Source: &types.ImageSourceMemberBytes{
					Value: data,
				},
			},
		}, nil
	}

	if slices.Contains(types.VideoFormat("").Values(), types.VideoFormat(format)) {
		return &types.ContentBlockMemberVideo{
			Value: types.VideoBlock{
				Format: types.VideoFormat(format),
				Source: &types.VideoSourceMemberBytes{
					Value: data,
				},
			},
		}, nil
	}

	if slices.Contains(types.DocumentFormat("").Values(), types.DocumentFormat(format)) {
		return &types.ContentBlockMemberDocument{
			Value: types.DocumentBlock{
				Name: aws.String(strings.TrimSuffix(path.Base(val.Name), path.Ext(val.Name))),

				Format: types.DocumentFormat(format),
				Source: &types.DocumentSourceMemberBytes{
					Value: data,
				},
			},
		}, nil
	}

	return nil, errors.New("unsupported file format")
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

func toContent(val types.ConverseOutput) string {
	message, ok := val.(*types.ConverseOutputMemberMessage)

	if !ok {
		return ""
	}

	for _, b := range message.Value.Content {
		switch block := b.(type) {
		case *types.ContentBlockMemberText:
			text := block.Value

			if text != "" {
				return text
			}
		}
	}

	return ""
}

func toToolCalls(val types.ConverseOutput) []provider.ToolCall {
	message, ok := val.(*types.ConverseOutputMemberMessage)

	if !ok {
		return nil
	}

	var result []provider.ToolCall

	for _, b := range message.Value.Content {
		switch block := b.(type) {
		case *types.ContentBlockMemberToolUse:
			data, _ := block.Value.Input.MarshalSmithyDocument()

			tool := provider.ToolCall{
				ID:   aws.ToString(block.Value.ToolUseId),
				Name: aws.ToString(block.Value.Name),

				Arguments: string(data),
			}

			result = append(result, tool)
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}
