package cohere

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/to"

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
		ID:    resp.Id,
		Model: c.model,

		Reason: toCompletionReason(resp.FinishReason),

		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: fromAssistantMessageContent(resp.Message),
		},

		Usage: toUsage(resp.Usage),
	}, nil
}

func (c *Completer) completeStream(ctx context.Context, req *v2.V2ChatStreamRequest, options *provider.CompleteOptions) (*provider.Completion, error) {
	result := provider.CompletionAccumulator{}

	var id string

	stream, err := c.client.ChatStream(ctx, req)

	if err != nil {
		return nil, err
	}

	defer stream.Close()

	for {
		resp, err := stream.Recv()

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			continue
		}

		if resp.MessageStart != nil {
			id = *resp.MessageStart.Id
		}

		if resp.ContentStart != nil {
			delta := provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			if resp.ContentStart.Delta != nil && resp.ContentStart.Delta.Message != nil && resp.ContentStart.Delta.Message.Content != nil && resp.ContentStart.Delta.Message.Content.Text != nil {
				content := *resp.ContentStart.Delta.Message.Content.Text
				delta.Message.Content = append(delta.Message.Content, provider.TextContent(content))
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if resp.ContentDelta != nil {
			delta := provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			if resp.ContentDelta.Delta != nil && resp.ContentDelta.Delta.Message != nil && resp.ContentDelta.Delta.Message.Content != nil && resp.ContentDelta.Delta.Message.Content.Text != nil {
				content := *resp.ContentDelta.Delta.Message.Content.Text
				delta.Message.Content = append(delta.Message.Content, provider.TextContent(content))
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if resp.ContentEnd != nil {
		}

		if resp.MessageEnd != nil {
			delta := provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			if resp.MessageEnd.Delta != nil && resp.MessageEnd.Delta.FinishReason != nil {
				reason := toCompletionReason(*resp.MessageEnd.Delta.FinishReason)

				if reason == "" {
					reason = provider.CompletionReasonStop
				}

				delta.Reason = reason
			}

			if resp.MessageEnd.Delta != nil && resp.MessageEnd.Delta.Usage != nil {
				delta.Usage = toUsage(resp.MessageEnd.Delta.Usage)
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if resp.ToolCallStart != nil {
			delta := provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			if resp.ToolCallStart.Delta != nil && resp.ToolCallStart.Delta.Message != nil && resp.ToolCallStart.Delta.Message.ToolCalls != nil {
				c := resp.ToolCallStart.Delta.Message.ToolCalls

				if c.Function != nil {
					call := provider.ToolCall{}

					if c.Id != nil {
						call.ID = *c.Id
					}

					if c.Function.Name != nil {
						call.Name = *c.Function.Name
					}

					if c.Function.Arguments != nil {
						call.Arguments = *c.Function.Arguments
					}

					delta.Message.Content = []provider.Content{
						provider.ToolCallContent(call),
					}
				}
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if resp.ToolCallDelta != nil {
			delta := provider.Completion{
				ID:    id,
				Model: c.model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
				},
			}

			if resp.ToolCallDelta.Delta != nil && resp.ToolCallDelta.Delta.Message != nil && resp.ToolCallDelta.Delta.Message.ToolCalls != nil {
				c := resp.ToolCallDelta.Delta.Message.ToolCalls

				if c.Function != nil {
					call := provider.ToolCall{}

					if c.Function.Arguments != nil {
						call.Arguments = *c.Function.Arguments
					}

					delta.Message.Content = []provider.Content{
						provider.ToolCallContent(call),
					}
				}
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}
	}

	return result.Result(), nil
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
			content := m.Text()

			message := &v2.ChatMessageV2{
				System: &v2.SystemMessage{
					Content: &v2.SystemMessageContent{
						String: content,
					},
				},
			}

			req.Messages = append(req.Messages, message)
		}

		if m.Role == provider.MessageRoleUser {
			if toolID, toolData, ok := m.ToolResult(); ok {
				var data any
				json.Unmarshal([]byte(toolData), &data)

				var parameters map[string]any

				if val, ok := data.(map[string]any); ok {
					parameters = val
				}

				if val, ok := data.([]any); ok {
					parameters = map[string]any{"data": val}
				}

				content, _ := json.Marshal(parameters)

				message := &v2.ChatMessageV2{
					Tool: &v2.ToolMessageV2{
						ToolCallId: toolID,

						Content: &v2.ToolMessageV2Content{
							String: string(content),
						},
					},
				}

				req.Messages = append(req.Messages, message)
			} else {
				content := m.Text()

				message := &v2.ChatMessageV2{
					User: &v2.UserMessage{
						Content: &v2.UserMessageContent{
							String: content,
						},
					},
				}

				req.Messages = append(req.Messages, message)
			}
		}

		if m.Role == provider.MessageRoleAssistant {
			message := &v2.ChatMessageV2{
				Assistant: &v2.AssistantMessage{},
			}

			for _, c := range m.Content {
				if c.Text != "" {
					if message.Assistant.Content == nil {
						message.Assistant.Content = &v2.AssistantMessageContent{
							AssistantMessageContentItemList: []*v2.AssistantMessageContentItem{},
						}
					}

					message.Assistant.Content.AssistantMessageContentItemList = append(message.Assistant.Content.AssistantMessageContentItemList, &v2.AssistantMessageContentItem{
						Text: &v2.TextContent{
							Text: c.Text,
						},
					})
				}

				if c.ToolCall != nil {
					call := &v2.ToolCallV2{
						Id:   to.Ptr(c.ToolCall.ID),
						Type: to.Ptr("function"),

						Function: &v2.ToolCallV2Function{
							Name:      to.Ptr(c.ToolCall.Name),
							Arguments: to.Ptr(c.ToolCall.Arguments),
						},
					}

					message.Assistant.ToolCalls = append(message.Assistant.ToolCalls, call)
				}
			}

			if len(message.Assistant.ToolCalls) > 0 {
				message.Assistant.Content = nil
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

func toUsage(usage *v2.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}

	if usage.Tokens != nil {
		return &provider.Usage{
			InputTokens:  int(*usage.Tokens.InputTokens),
			OutputTokens: int(*usage.Tokens.OutputTokens),
		}
	}

	return nil
}

func fromAssistantMessageContent(val *v2.AssistantMessageResponse) []provider.Content {
	if val == nil {
		return nil
	}

	var parts []provider.Content

	for _, c := range val.Content {
		if c.Text != nil {
			parts = append(parts, provider.Content{
				Text: c.Text.Text,
			})
		}
	}

	return parts
}
