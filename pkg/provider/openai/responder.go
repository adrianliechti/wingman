package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"iter"
	"slices"

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

func (r *Responder) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options == nil {
			options = new(provider.CompleteOptions)
		}

		req, err := r.convertResponsesRequest(messages, options)

		if err != nil {
			yield(nil, err)
			return
		}

		stream := r.responses.NewStreaming(ctx, *req)

		for stream.Next() {
			data := stream.Current()

			switch event := data.AsAny().(type) {
			case responses.ResponseCreatedEvent:
			case responses.ResponseInProgressEvent:
			case responses.ResponseOutputItemAddedEvent:
				switch item := event.Item.AsAny().(type) {
				case responses.ResponseFunctionToolCall:
					delta := &provider.Completion{
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

					if !yield(delta, nil) {
						return
					}
				}

			case responses.ResponseContentPartAddedEvent:
			case responses.ResponseTextDeltaEvent:
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.TextContent(event.Delta),
						},
					},
				}

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseReasoningTextDeltaEvent:
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ReasoningContent(provider.Reasoning{
								Text: event.Delta,
							}),
						},
					},
				}

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseReasoningSummaryTextDeltaEvent:
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ReasoningContent(provider.Reasoning{
								Summary: event.Delta,
							}),
						},
					},
				}

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseTextDoneEvent:
			case responses.ResponseFunctionCallArgumentsDeltaEvent:
				delta := &provider.Completion{
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

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseFunctionCallArgumentsDoneEvent:
			case responses.ResponseContentPartDoneEvent:
			case responses.ResponseOutputItemDoneEvent:
			case responses.ResponseCompletedEvent:
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Usage: toResponseUsage(event.Response.Usage),
				}

				if !yield(delta, nil) {
					return
				}

			default:
				println("unknown event", data.Type)
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
	}
}

func (r *Responder) convertResponsesRequest(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParams, error) {
	if slices.Contains(ReasoningModels, r.model) {
		options.Temperature = nil
	}

	input, err := r.convertResponsesInput(messages)

	if err != nil {
		return nil, err
	}

	tools, err := r.convertResponsesTools(options.Tools)

	if err != nil {
		return nil, err
	}

	// tools = append(tools, responses.ToolUnionParam{
	// 	OfCodeInterpreter: &responses.ToolCodeInterpreterParam{
	// 		Container: responses.ToolCodeInterpreterContainerUnionParam{
	// 			OfCodeInterpreterContainerAuto: &responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{},
	// 		},
	// 	},
	// })

	// tools = append(tools, responses.ToolUnionParam{
	// 	OfWebSearch: &responses.WebSearchToolParam{
	// 		Type: responses.WebSearchToolTypeWebSearch,
	// 	},
	// })

	req := &responses.ResponseNewParams{
		Model: r.model,

		Store: openai.Bool(false),

		Input: input,
		Tools: tools,

		Truncation: responses.ResponseNewParamsTruncationAuto,
	}

	if slices.Contains(CodingModels, r.model) {
		req.Truncation = ""
	}

	switch options.Effort {
	case provider.EffortMinimal:
		req.Reasoning.Effort = responses.ReasoningEffortMinimal
		req.Reasoning.Summary = responses.ReasoningSummaryAuto
	case provider.EffortLow:
		req.Reasoning.Effort = responses.ReasoningEffortLow
		req.Reasoning.Summary = responses.ReasoningSummaryAuto
	case provider.EffortMedium:
		req.Reasoning.Effort = responses.ReasoningEffortMedium
		req.Reasoning.Summary = responses.ReasoningSummaryAuto
	case provider.EffortHigh:
		req.Reasoning.Effort = responses.ReasoningEffortHigh
		req.Reasoning.Summary = responses.ReasoningSummaryAuto
	}

	switch options.Verbosity {
	case provider.VerbosityLow:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityLow
	case provider.VerbosityMedium:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityMedium
	case provider.VerbosityHigh:
		req.Text.Verbosity = responses.ResponseTextConfigVerbosityHigh
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

func (r *Responder) convertResponsesInput(messages []provider.Message) (responses.ResponseNewParamsInputUnion, error) {
	var result []responses.ResponseInputItemUnionParam

	for _, m := range messages {
		switch m.Role {
		case provider.MessageRoleSystem:
			message := &responses.ResponseInputItemMessageParam{
				Role: string(responses.ResponseInputMessageItemRoleSystem),
			}

			if slices.Contains(ReasoningModels, r.model) {
				message.Role = string(responses.ResponseInputMessageItemRoleDeveloper)
			}

			for _, c := range m.Content {
				if c.Text != "" {
					message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
						OfInputText: &responses.ResponseInputTextParam{
							Text: c.Text,
						},
					})
				}
			}

			if len(message.Content) > 0 {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfInputMessage: message,
				})
			}

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

						name := c.File.Name

						if name == "" {
							name = "file.pdf"
						}

						message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
							OfInputFile: &responses.ResponseInputFileParam{
								Filename: openai.String(name),
								FileData: openai.String(url),
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

func (r *Responder) convertResponsesTools(tools []provider.Tool) ([]responses.ToolUnionParam, error) {
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
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
	}
}
