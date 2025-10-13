package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
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

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},
	}

	for _, item := range resp.Output {
		switch item := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, c := range item.Content {
				if c.JSON.Text.Valid() {
					content := provider.TextContent(c.Text)
					result.Message.Content = append(result.Message.Content, content)
				}
			}
		case responses.ResponseFunctionToolCall:
			call := provider.ToolCall{
				ID: item.CallID,

				Name:      item.Name,
				Arguments: item.Arguments,
			}

			result.Message.Content = append(result.Message.Content, provider.ToolCallContent(call))
		}
	}

	return result, nil
}

func (r *Responder) completeStream(ctx context.Context, req responses.ResponseNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := r.responses.NewStreaming(ctx, req)

	result := provider.CompletionAccumulator{}

	for stream.Next() {
		data := stream.Current()

		//println(data.RawJSON())

		switch event := data.AsAny().(type) {
		case responses.ResponseCreatedEvent:
		case responses.ResponseInProgressEvent:
		case responses.ResponseOutputItemAddedEvent:
			switch item := event.Item.AsAny().(type) {
			case responses.ResponseFunctionToolCall:
				delta := provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								ID: item.CallID,

								Name:      item.Name,
								Arguments: item.Arguments,
							}),
						},
					},
				}

				result.Add(delta)

				if err := options.Stream(ctx, delta); err != nil {
					return nil, err
				}
			}
		case responses.ResponseContentPartAddedEvent:
		case responses.ResponseTextDeltaEvent:
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.TextContent(event.Delta),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		case responses.ResponseTextDoneEvent:
		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.ToolCallContent(provider.ToolCall{
							Arguments: event.Delta,
						}),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		case responses.ResponseFunctionCallArgumentsDoneEvent:
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,

					Content: []provider.Content{
						provider.ToolCallContent(provider.ToolCall{}),
					},
				},
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		case responses.ResponseContentPartDoneEvent:
		case responses.ResponseOutputItemDoneEvent:
		case responses.ResponseCompletedEvent:
			delta := provider.Completion{
				ID:    data.Response.ID,
				Model: data.Response.Model,

				Usage: toResponseUsage(event.Response.Usage),
			}

			result.Add(delta)

			if err := options.Stream(ctx, delta); err != nil {
				return nil, err
			}
		default:
			println("unknown event", data.Type)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return result.Result(), nil
}

func (r *Responder) convertResponsesRequest(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParams, error) {
	models := []string{
		"o1",
		"o1-mini",
		"o3",
		"o3-mini",
		"o4",
		"o4-mini",

		"gpt-5",
	}

	if slices.Contains(models, r.model) {
		options.Temperature = nil
	}

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
	case provider.EffortMinimal:
		req.Reasoning.Effort = responses.ReasoningEffortMinimal
	case provider.EffortLow:
		req.Reasoning.Effort = responses.ReasoningEffortLow
	case provider.EffortMedium:
		req.Reasoning.Effort = responses.ReasoningEffortMedium
	case provider.EffortHigh:
		req.Reasoning.Effort = responses.ReasoningEffortHigh
	}

	switch options.Verbosity {
	case provider.VerbosityLow:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityLow
	case provider.VerbosityMedium:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityMedium
	case provider.VerbosityHigh:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityHigh
	}

	if options.Format == provider.CompletionFormatJSON {
		req.Text.Format = responses.ResponseFormatTextConfigUnionParam{
			OfJSONObject: &responses.ResponseFormatJSONObjectParam{},
		}
	}

	if options.Schema != nil {
		schema := &responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   options.Schema.Name,
			Schema: options.Schema.Schema,
		}

		if options.Schema.Strict != nil {
			schema.Strict = openai.Bool(*options.Schema.Strict)
		}

		if options.Schema.Description != "" {
			schema.Description = openai.String(options.Schema.Description)
		}

		req.Text.Format = responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: schema,
		}
	}

	if options.MaxTokens != nil {
		req.MaxOutputTokens = openai.Int(int64(*options.MaxTokens))
	}

	if options.Temperature != nil {
		req.Temperature = openai.Float(float64(*options.Temperature))
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

						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.String(c.ToolResult.Data),
						},
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

func toResponseUsage(usage responses.ResponseUsage) *provider.Usage {
	if usage.TotalTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
	}
}
