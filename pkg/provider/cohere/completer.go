package cohere

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/to"
	"github.com/google/uuid"

	v2 "github.com/cohere-ai/cohere-go/v2"
	client "github.com/cohere-ai/cohere-go/v2/v2"
)

var _ provider.Completer = (*Completer)(nil)

type Completer struct {
	*Config
	client *client.Client
}

func NewCompleter(model string, options ...Option) (*Completer, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Completer{
		Config: cfg,
		client: client.NewClient(cfg.Options()...),
	}, nil
}

func (c *Completer) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := convertChatRequest(c.model, messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		req := &v2.V2ChatStreamRequest{
			Model: c.model,

			Tools:    req.Tools,
			Messages: req.Messages,

			ResponseFormat: req.ResponseFormat,

			MaxTokens:     req.MaxTokens,
			StopSequences: req.StopSequences,
			Temperature:   req.Temperature,
		}

		return c.completeStream(ctx, req, options)
	}

	return c.complete(ctx, req, options)
}

func (c *Completer) complete(ctx context.Context, req *v2.V2ChatRequest, options *provider.CompleteOptions) (*provider.Completion, error) {
	resp, err := c.client.Chat(ctx, req)

	if err != nil {
		return nil, convertError(err)
	}

	return &provider.Completion{
		ID:     resp.Id,
		Reason: toCompletionReason(resp.FinishReason),

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: fromAssistantMessageContent(resp.Message),
		},
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req *v2.V2ChatStreamRequest, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream, err := c.client.ChatStream(ctx, req)

	if err != nil {
		return nil, err
	}

	defer stream.Close()

	result := &provider.Completion{
		ID: uuid.New().String(),

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},

		//Usage: &provider.Usage{},
	}

	resultToolID := ""
	resultToolCalls := map[string]provider.ToolCall{}

	for {
		resp, err := stream.Recv()

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			continue
		}

		if resp.MessageStart != nil {
			result.ID = *resp.MessageStart.Id
		}

		if resp.ContentStart != nil {
			if resp.ContentStart.Delta != nil && resp.ContentStart.Delta.Message != nil && resp.ContentStart.Delta.Message.Content != nil && resp.ContentStart.Delta.Message.Content.Text != nil {
				content := *resp.ContentStart.Delta.Message.Content.Text
				result.Message.Content += content

				if len(content) > 0 {
					delta := provider.Completion{
						ID: result.ID,

						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: content,
						},
					}

					if err := options.Stream(ctx, delta); err != nil {
						return nil, err
					}
				}
			}
		}

		if resp.ContentDelta != nil {
			if resp.ContentDelta.Delta != nil && resp.ContentDelta.Delta.Message != nil && resp.ContentDelta.Delta.Message.Content != nil && resp.ContentDelta.Delta.Message.Content.Text != nil {
				content := *resp.ContentDelta.Delta.Message.Content.Text
				result.Message.Content += content

				if len(content) > 0 {
					delta := provider.Completion{
						ID: result.ID,

						Message: &provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: content,
						},
					}

					if err := options.Stream(ctx, delta); err != nil {
						return nil, err
					}
				}
			}
		}

		if resp.ContentEnd != nil {
		}

		if resp.MessageEnd != nil {
			reson := resp.MessageEnd.Delta.FinishReason

			delta := provider.Completion{
				ID: result.ID,

				Message: &provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: "",
				},
			}

			if resp.MessageEnd.Delta != nil && reson != nil {
				result.Reason = toCompletionReason(*reson)
			}

			if delta.Reason == "" {
				delta.Reason = provider.CompletionReasonStop
			}

			if resp.MessageEnd.Delta != nil && resp.MessageEnd.Delta.Usage != nil && resp.MessageEnd.Delta.Usage.BilledUnits != nil {
				usage := &provider.Usage{}

				delta.Usage = usage
				result.Usage = usage

				if resp.MessageEnd.Delta.Usage.BilledUnits.InputTokens != nil {
					usage.InputTokens = int(*resp.MessageEnd.Delta.Usage.BilledUnits.InputTokens)
				}

				if resp.MessageEnd.Delta.Usage.BilledUnits.OutputTokens != nil {
					usage.OutputTokens = int(*resp.MessageEnd.Delta.Usage.BilledUnits.OutputTokens)
				}
			}

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if resp.ToolCallStart != nil {
			if resp.ToolCallStart.Delta != nil && resp.ToolCallStart.Delta.Message != nil && resp.ToolCallStart.Delta.Message.ToolCalls != nil {
				call := resp.ToolCallStart.Delta.Message.ToolCalls

				tool := provider.ToolCall{}

				if call.Id != nil {
					tool.ID = *call.Id
				}

				if call.Function != nil {
					if call.Function.Name != nil {
						tool.Name = *call.Function.Name
					}

					if call.Function.Arguments != nil {
						tool.Arguments = *call.Function.Arguments
					}
				}

				if tool.ID != "" {
					resultToolID = tool.ID
					resultToolCalls[tool.ID] = tool
				}

				delta := provider.Completion{
					ID: result.ID,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						ToolCalls: []provider.ToolCall{tool},
					},
				}

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}
			}
		}

		if resp.ToolCallDelta != nil {
			if resp.ToolCallDelta.Delta != nil && resp.ToolCallDelta.Delta.Message != nil && resp.ToolCallDelta.Delta.Message.ToolCalls != nil {
				call := resp.ToolCallDelta.Delta.Message.ToolCalls

				tool := provider.ToolCall{}

				if call.Function != nil {
					if call.Function.Arguments != nil {
						tool.Arguments = *call.Function.Arguments
					}
				}

				if t, ok := resultToolCalls[resultToolID]; ok {
					t.Arguments += tool.Arguments
					resultToolCalls[resultToolID] = t
				}

				delta := provider.Completion{
					ID: result.ID,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						ToolCalls: []provider.ToolCall{tool},
					},
				}

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}
			}
		}

		if resp.ToolCallEnd != nil {
			resultToolID = ""
		}
	}

	if len(resultToolCalls) > 0 {
		result.Message.ToolCalls = to.Values(resultToolCalls)
	}

	return result, nil
}

func convertChatRequest(model string, messages []provider.Message, options *provider.CompleteOptions) (*v2.V2ChatRequest, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req := &v2.V2ChatRequest{
		Model: model,
	}

	if options.Stop != nil {
		req.StopSequences = options.Stop
	}

	if options.MaxTokens != nil {
		req.MaxTokens = options.MaxTokens
	}

	if options.Temperature != nil {
		req.Temperature = to.Ptr(float64(*options.Temperature))
	}

	for _, t := range options.Tools {
		tool := &v2.ToolV2{
			Type: to.Ptr("function"),

			Function: &v2.ToolV2Function{
				Name:        t.Name,
				Description: to.Ptr(t.Description),

				Parameters: t.Parameters,
			},
		}

		req.Tools = append(req.Tools, tool)
	}

	for _, m := range messages {
		switch m.Role {

		case provider.MessageRoleSystem:
			message := &v2.ChatMessageV2{
				Role: "system",

				System: &v2.SystemMessage{
					Content: &v2.SystemMessageContent{
						String: m.Content.String(),
					},
				},
			}

			req.Messages = append(req.Messages, message)
		}

		if m.Role == provider.MessageRoleUser {
			message := &v2.ChatMessageV2{
				Role: "user",

				User: &v2.UserMessage{
					Content: &v2.UserMessageContent{
						String: m.Content,
					},
				},
			}

			req.Messages = append(req.Messages, message)
		}

		if m.Role == provider.MessageRoleAssistant {
			message := &v2.ChatMessageV2{
				Role: "assistant",

				Assistant: &v2.AssistantMessage{},
			}

			if m.Content != "" {
				message.Assistant.Content = &v2.AssistantMessageContent{
					String: m.Content,
				}
			}

			for _, t := range m.ToolCalls {
				call := &v2.ToolCallV2{
					Id:   to.Ptr(t.ID),
					Type: to.Ptr("function"),

					Function: &v2.ToolCallV2Function{
						Name:      to.Ptr(t.Name),
						Arguments: to.Ptr(t.Arguments),
					},
				}

				message.Assistant.ToolCalls = append(message.Assistant.ToolCalls, call)
			}

			req.Messages = append(req.Messages, message)
		}

		if m.Role == provider.MessageRoleTool {
			var data any
			json.Unmarshal([]byte(m.Content), &data)

			var parameters map[string]any

			if val, ok := data.(map[string]any); ok {
				parameters = val
			}

			if val, ok := data.([]any); ok {
				parameters = map[string]any{"data": val}
			}

			content, _ := json.Marshal(parameters)

			message := &v2.ChatMessageV2{
				Role: "tool",

				Tool: &v2.ToolMessageV2{
					ToolCallId: m.Tool,

					Content: &v2.ToolMessageV2Content{
						String: string(content),
					},
				},
			}

			req.Messages = append(req.Messages, message)
		}
	}

	return req, nil
}

func toCompletionReason(reason v2.ChatFinishReason) provider.CompletionReason {
	switch reason {
	case v2.ChatFinishReasonComplete:
		return provider.CompletionReasonStop

	case v2.ChatFinishReasonStopSequence:
		return provider.CompletionReasonStop

	case v2.ChatFinishReasonMaxTokens:
		return provider.CompletionReasonLength

	case v2.ChatFinishReasonToolCall:
		return provider.CompletionReasonTool

	case v2.ChatFinishReasonError:
		return ""
	}

	return ""
}

func fromAssistantMessageContent(val *v2.AssistantMessageResponse) []provider.Content {
	if val == nil {
		return nil
	}

	var parts []provider.Content

	for _, c := range val.Content {
		if c.Text != nil {
			parts = append(parts, provider.Content{
				Text1: c.Text.Text,
			})
		}
	}

	return parts
}
