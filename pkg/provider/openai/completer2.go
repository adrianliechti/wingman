package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

var _ provider.Completer = (*Completer2)(nil)

type Completer2 struct {
	*Config
	responses responses.ResponseService
}

func NewCompleter2(url, model string, options ...Option) (*Completer2, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Completer2{
		Config:    cfg,
		responses: responses.NewResponseService(cfg.Options()...),
	}, nil
}

func (c *Completer2) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) (*provider.Completion, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	req, err := c.convertResponseRequest(messages, options)

	if err != nil {
		return nil, err
	}

	if options.Stream != nil {
		return c.completeStream(ctx, *req, options)
	}

	return c.complete(ctx, *req, options)
}

func (c *Completer2) complete(ctx context.Context, req responses.ResponseNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	response, err := c.responses.New(ctx, req)

	if err != nil {
		return nil, convertError(err)
	}

	result := &provider.Completion{
		ID:    response.ID,
		Model: c.model,

		Reason: provider.CompletionReasonStop,

		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
		},
	}

	// if val := toCompletionResult(choice.FinishReason); val != "" {
	// 	result.Reason = val
	// }

	if val := toUsage2(response.Usage); val != nil {
		result.Usage = val
	}

	for _, output := range response.Output {
		switch output := output.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, content := range output.Content {
				switch content := content.AsAny().(type) {
				case responses.ResponseOutputText:
					result.Message.Content = append(result.Message.Content, provider.TextContent(content.Text))

				case responses.ResponseOutputRefusal:
					result.Message.Content = append(result.Message.Content, provider.RefusalContent(content.Refusal))
				}
			}

		case responses.ResponseFunctionToolCall:
			call := provider.ToolCall{
				ID: output.ID,

				Name:      output.Name,
				Arguments: output.Arguments,
			}

			result.Message.Content = append(result.Message.Content, provider.ToolCallContent(call))
		}
	}

	return result, nil
}

func (c *Completer2) completeStream(ctx context.Context, req responses.ResponseNewParams, options *provider.CompleteOptions) (*provider.Completion, error) {
	stream := c.responses.NewStreaming(ctx, req)

	result := provider.CompletionAccumulator{}

	for stream.Next() {
		event := stream.Current()

		var delta provider.Completion

		switch event := event.AsAny().(type) {
		case responses.ResponseCreatedEvent:
		case responses.ResponseTextDeltaEvent:
			delta = provider.Completion{
				ID: event.ItemID,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: []provider.Content{

						provider.TextContent(event.Delta),
					},
				},
			}

		case responses.ResponseRefusalDeltaEvent:
			delta = provider.Completion{
				ID: event.ItemID,

				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: []provider.Content{

						provider.RefusalContent(event.Delta),
					},
				},
			}

		case responses.ResponseFunctionCallArgumentsDeltaEvent:
		default:
			println("unknown event type:", event)
		}

		// if len(chunk.Choices) > 0 {
		// 	choice := chunk.Choices[0]

		// 	delta.Reason = toCompletionResult(choice.FinishReason)

		// 	if choice.Delta.JSON.Content.Valid() {
		// 		delta.Message.Content = append(delta.Message.Content, provider.TextContent(choice.Delta.Content))
		// 	}

		// 	if choice.Delta.JSON.Refusal.Valid() {
		// 		delta.Message.Content = append(delta.Message.Content, provider.TextContent(choice.Delta.Refusal))
		// 	}

		// 	for _, c := range choice.Delta.ToolCalls {
		// 		call := provider.ToolCall{
		// 			ID: c.ID,

		// 			Name:      c.Function.Name,
		// 			Arguments: c.Function.Arguments,
		// 		}

		// 		delta.Message.Content = append(delta.Message.Content, provider.ToolCallContent(call))
		// 	}
		// }

		result.Add(delta)

		if err := options.Stream(ctx, delta); err != nil {
			return nil, err
		}
	}

	if err := stream.Err(); err != nil {
		return nil, convertError(err)
	}

	return result.Result(), nil
}

func (c *Completer2) convertResponseRequest(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParams, error) {
	if options == nil {
		options = new(provider.CompleteOptions)
	}

	input, err := c.convertInput(messages, options)

	if err != nil {
		return nil, err
	}

	req := &responses.ResponseNewParams{
		Store: openai.Bool(false),

		Model: c.model,

		Input: *input,
	}

	switch options.Effort {
	case provider.ReasoningEffortLow:
		req.Reasoning.Effort = shared.ReasoningEffortLow

	case provider.ReasoningEffortMedium:
		req.Reasoning.Effort = shared.ReasoningEffortMedium

	case provider.ReasoningEffortHigh:
		req.Reasoning.Effort = shared.ReasoningEffortHigh
	}

	// if options.Format == provider.CompletionFormatJSON {
	// 	req.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
	// 		OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
	// 	}
	// }

	// if options.Schema != nil {
	// 	schema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
	// 		Name:   options.Schema.Name,
	// 		Schema: options.Schema.Schema,
	// 	}

	// 	if options.Schema.Description != "" {
	// 		schema.Description = openai.String(options.Schema.Description)
	// 	}

	// 	if options.Schema.Strict != nil {
	// 		schema.Strict = openai.Bool(*options.Schema.Strict)
	// 	}

	// 	req.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
	// 		OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
	// 			JSONSchema: schema,
	// 		},
	// 	}
	// }

	// if options.Stop != nil {
	// 	req.Stop = openai.ChatCompletionNewParamsStopUnion{
	// 		OfStringArray: options.Stop,
	// 	}
	// }

	// if options.MaxTokens != nil {
	// 	models := []string{
	// 		"o1",
	// 		"o1-mini",
	// 		"o3",
	// 		"o3-mini",
	// 		"o4",
	// 		"o4-mini",
	// 	}

	// 	if slices.Contains(models, c.model) {
	// 		req.MaxCompletionTokens = openai.Int(int64(*options.MaxTokens))
	// 	} else {
	// 		req.MaxTokens = openai.Int(int64(*options.MaxTokens))
	// 	}
	// }

	// if options.Temperature != nil {
	// 	req.Temperature = openai.Float(float64(*options.Temperature))
	// }

	return req, nil
}

func (c *Completer2) convertInput(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParamsInputUnion, error) {
	var input responses.ResponseInputParam

	for _, m := range messages {
		switch m.Role {
		case provider.MessageRoleSystem:
			role := "system"

			if slices.Contains([]string{"o1", "o1-mini", "o3-mini", "o4-mini"}, c.model) {
				role = "developer"
			}

			var content []responses.ResponseInputContentUnionParam

			for _, c := range m.Content {
				if c.Text != "" {
					content = append(content, responses.ResponseInputContentUnionParam{
						OfInputText: &responses.ResponseInputTextParam{
							Text: c.Text,
						},
					})
				}
			}

			input = append(input, responses.ResponseInputItemUnionParam{
				OfInputMessage: &responses.ResponseInputItemMessageParam{
					Role:    role,
					Content: content,
				},
			})

		case provider.MessageRoleUser:
			var content []responses.ResponseInputContentUnionParam

			for _, c := range m.Content {
				if c.Text != "" {
					content = append(content, responses.ResponseInputContentUnionParam{
						OfInputText: &responses.ResponseInputTextParam{
							Text: m.Text(),
						},
					})
				}

				if c.File != nil {
					mime := c.File.ContentType
					data := base64.StdEncoding.EncodeToString(c.File.Content)

					switch c.File.ContentType {
					case "image/png", "image/jpeg", "image/webp", "image/gif":
						content = append(content, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String("data:" + mime + ";base64," + data),
							},
						})

					case "application/pdf":
						content = append(content, responses.ResponseInputContentUnionParam{
							OfInputFile: &responses.ResponseInputFileParam{
								Filename: openai.String(c.File.Name),
								FileData: openai.String("data:" + mime + ";base64," + data),
							},
						})

					default:
						return nil, errors.New("unsupported content type")
					}
				}

				if c.ToolResult != nil {
					return nil, errors.New("tool results are not supported in this context")
				}
			}

			input = append(input, responses.ResponseInputItemUnionParam{
				OfInputMessage: &responses.ResponseInputItemMessageParam{
					Role:    "user",
					Content: content,
				},
			})

		case provider.MessageRoleAssistant:
			var content []responses.ResponseOutputMessageContentUnionParam

			for _, c := range m.Content {
				if c.Text != "" {
					content = append(content, responses.ResponseOutputMessageContentUnionParam{
						OfOutputText: &responses.ResponseOutputTextParam{
							Text: c.Text,
						},
					})
				}

				if c.Refusal != "" {
					content = append(content, responses.ResponseOutputMessageContentUnionParam{
						OfRefusal: &responses.ResponseOutputRefusalParam{
							Refusal: c.Refusal,
						},
					})
				}

				if c.ToolCall != nil {
					return nil, errors.New("tool calls are not supported in this context")
				}
			}

			input = append(input, responses.ResponseInputItemUnionParam{
				OfOutputMessage: &responses.ResponseOutputMessageParam{
					Role:    "assistant",
					Content: content,
				},
			})
		}
	}

	// 	case provider.MessageRoleAssistant:
	// 		message := openai.ChatCompletionAssistantMessageParam{}

	// 		var content []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion

	// 		for _, c := range m.Content {
	// 			if c.Text != "" {
	// 				content = append(content, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
	// 					OfText: &openai.ChatCompletionContentPartTextParam{
	// 						Text: c.Text,
	// 					},
	// 				})
	// 			}

	// 			if c.Refusal != "" {
	// 				content = append(content, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
	// 					OfRefusal: &openai.ChatCompletionContentPartRefusalParam{
	// 						Refusal: c.Refusal,
	// 					},
	// 				})
	// 			}

	// 			if c.ToolCall != nil {
	// 				call := openai.ChatCompletionMessageToolCallParam{
	// 					ID: c.ToolCall.ID,

	// 					Function: openai.ChatCompletionMessageToolCallFunctionParam{
	// 						Name:      c.ToolCall.Name,
	// 						Arguments: c.ToolCall.Arguments,
	// 					},
	// 				}

	// 				message.ToolCalls = append(message.ToolCalls, call)
	// 			}
	// 		}

	// 		if len(content) > 0 {
	// 			message.Content.OfArrayOfContentParts = content
	// 		}

	// 		result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &message})
	// 	}
	// }

	return &responses.ResponseNewParamsInputUnion{
		OfInputItemList: input,
	}, nil
}

func convertTools2(tools []provider.Tool) ([]openai.ChatCompletionToolParam, error) {
	var result []openai.ChatCompletionToolParam

	for _, t := range tools {
		if t.Name == "" {
			continue
		}

		function := openai.FunctionDefinitionParam{
			Name: t.Name,

			Parameters: openai.FunctionParameters(t.Parameters),
		}

		if t.Description != "" {
			function.Description = openai.String(t.Description)
		}

		if t.Strict != nil {
			function.Strict = openai.Bool(*t.Strict)
		}

		tool := openai.ChatCompletionToolParam{
			Function: function,
		}

		result = append(result, tool)
	}

	return result, nil
}

func toCompletionResult2(val string) provider.CompletionReason {
	switch val {
	case "stop":
		return provider.CompletionReasonStop

	case "length":
		return provider.CompletionReasonLength

	case "tool_calls":
		return provider.CompletionReasonTool

	case "content_filter":
		return provider.CompletionReasonFilter

	default:
		return ""
	}
}

func toUsage2(metadata responses.ResponseUsage) *provider.Usage {
	if metadata.TotalTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(metadata.InputTokens),
		OutputTokens: int(metadata.OutputTokens),
	}
}
