package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/responses"
	"github.com/openai/openai-go/v2/shared"
)

var _ provider.Completer = (*Responder)(nil)

type Responder struct {
	*Config
	responses responses.ResponseService
}

func NewResponder(url, model string, options ...Option) (*Responder, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Responder{
		Config:    cfg,
		responses: responses.NewResponseService(cfg.Options()...),
	}, nil
}

func (r *Responder) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := r.convertResponsesRequest(messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		return r.completeStream(ctx, *req, options)
	}

	return r.complete(ctx, *req)
}

func (r *Responder) complete(ctx context.Context, req responses.ResponseNewParams) (*provider.Completion, error) {
	resp, err := r.responses.New(ctx, req)

	if err != nil {
		return nil, err
	}

	result := &provider.Completion{
		ID:    resp.ID,
		Model: resp.Model,

		Reason: provider.CompletionReasonStop,

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},
	}

	for _, item := range resp.Output {
		for _, c := range item.Content {
			if c.JSON.Text.Valid() {
				content := provider.TextContent(c.Text)
				result.Message.Content = append(result.Message.Content, content)
			}

			if c.JSON.Refusal.Valid() {
				content := provider.RefusalContent(c.Refusal)
				result.Message.Content = append(result.Message.Content, content)
			}
		}
	}

	return result, nil
}

func (r *Responder) completeStream(ctx context.Context, req responses.ResponseNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := r.responses.NewStreaming(ctx, req)

	result := provider.CompletionAccumulator{}

	for stream.Next() {
		data := stream.Current()

		if data.JSON.Delta.Valid() {
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.TextContent(data.Delta),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}

		if data.JSON.Refusal.Valid() {
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.RefusalContent(data.Refusal),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return result.Result(), nil
}

func (r *Responder) convertResponsesRequest(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParams, error) {
	input, err := convertResponsesInput(messages)

	if err != nil {
		return nil, err
	}

	tools, err := convertResponsesTools(options.Tools)

	if err != nil {
		return nil, err
	}

	req := &responses.ResponseNewParams{
		Model: r.model,

		Store: openai.Bool(false),

		Input: input,
		Tools: tools,

		Truncation: responses.ResponseNewParamsTruncationAuto,
	}

	if val := convertInstructions(messages); val != "" {
		req.Instructions = openai.String(val)
	}

	switch options.Effort {
	case provider.ReasoningEffortMinimal:
		req.Reasoning.Effort = shared.ReasoningEffortMinimal

	case provider.ReasoningEffortLow:
		req.Reasoning.Effort = shared.ReasoningEffortLow

	case provider.ReasoningEffortMedium:
		req.Reasoning.Effort = shared.ReasoningEffortMedium

	case provider.ReasoningEffortHigh:
		req.Reasoning.Effort = shared.ReasoningEffortHigh
	}

	return req, nil
}

func convertInstructions(messages []provider.Message) string {
	var result []string

	for _, m := range messages {
		if m.Role == provider.MessageRoleSystem {
			for _, c := range m.Content {
				if c.Text != "" {
					result = append(result, c.Text)
				}
			}
		}
	}

	return strings.Join(result, "\n\n")
}

func convertResponsesInput(messages []provider.Message) (responses.ResponseNewParamsInputUnion, error) {
	var result []responses.ResponseInputItemUnionParam

	for _, m := range messages {
		switch m.Role {
		case provider.MessageRoleSystem:
		case provider.MessageRoleUser:
			message := &responses.ResponseInputItemMessageParam{
				Role: string(responses.ResponseInputMessageItemRoleUser),
			}

			for _, c := range m.Content {
				if c.Text != "" {
					message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
						OfInputText: &responses.ResponseInputTextParam{
							Text: c.Text,
						},
					})
				}

				if c.File != nil {
					mime := c.File.ContentType
					content := base64.StdEncoding.EncodeToString(c.File.Content)

					switch c.File.ContentType {
					case "image/png", "image/jpeg", "image/webp", "image/gif":
						url := "data:" + mime + ";base64," + content

						message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(url),
							},
						})

					case "application/pdf":
						url := "data:" + mime + ";base64," + content

						message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
							OfInputFile: &responses.ResponseInputFileParam{
								FileURL: openai.String(url),
							},
						})

					default:
						return responses.ResponseNewParamsInputUnion{}, errors.New("unsupported content type")
					}
				}

				if c.ToolResult != nil {
					output := &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: c.ToolResult.ID,
						Output: c.ToolResult.Data,
					}

					result = append(result, responses.ResponseInputItemUnionParam{
						OfFunctionCallOutput: output,
					})
				}
			}

			if len(message.Content) > 0 {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfInputMessage: message,
				})
			}

		case provider.MessageRoleAssistant:
			message := &responses.ResponseOutputMessageParam{}

			for _, c := range m.Content {
				if c.Text != "" {
					message.Content = append(message.Content, responses.ResponseOutputMessageContentUnionParam{
						OfOutputText: &responses.ResponseOutputTextParam{
							Text: c.Text,
						},
					})
				}

				if c.Refusal != "" {
					message.Content = append(message.Content, responses.ResponseOutputMessageContentUnionParam{
						OfRefusal: &responses.ResponseOutputRefusalParam{
							Refusal: c.Refusal,
						},
					})
				}

				if c.ToolCall != nil {
					call := &responses.ResponseFunctionToolCallParam{
						CallID: c.ToolCall.ID,

						Name:      c.ToolCall.Name,
						Arguments: c.ToolCall.Arguments,
					}

					result = append(result, responses.ResponseInputItemUnionParam{
						OfFunctionCall: call,
					})
				}
			}

			if len(message.Content) > 0 {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfOutputMessage: message,
				})
			}
		}
	}

	return responses.ResponseNewParamsInputUnion{
		OfInputItemList: result,
	}, nil
}

func convertResponsesTools(tools []provider.Tool) ([]responses.ToolUnionParam, error) {
	var result []responses.ToolUnionParam

	for _, t := range tools {
		if t.Name == "" {
			continue
		}

		function := &responses.FunctionToolParam{
			Name: t.Name,

			Parameters: t.Parameters,
		}

		if t.Description != "" {
			function.Description = openai.String(t.Description)
		}

		if t.Strict != nil {
			function.Strict = openai.Bool(*t.Strict)
		}

		result = append(result, responses.ToolUnionParam{
			OfFunction: function,
		})
	}

	return result, nil
}
