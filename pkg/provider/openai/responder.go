package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"iter"
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

		// Maps item ID → call ID for function tool calls.
		// ResponseFunctionCallArgumentsDeltaEvent uses item_id, but downstream
		// consumers identify tool calls by call_id (used in function_call_output).
		itemToCallID := make(map[string]string)

		for stream.Next() {
			data := stream.Current()

			switch event := data.AsAny().(type) {
			case responses.ResponseCreatedEvent:
			case responses.ResponseInProgressEvent:
			case responses.ResponseOutputItemAddedEvent:
				switch item := event.Item.AsAny().(type) {
				case responses.ResponseFunctionToolCall:
					itemToCallID[item.ID] = item.CallID

					delta := &provider.Completion{
						ID:    data.Response.ID,
						Model: data.Response.Model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.ToolCallContent(provider.ToolCall{
									ID:   item.CallID,
									Name: item.Name,
								}),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case responses.ResponseApplyPatchToolCall:
					// Emit initial empty TextEditorCall — will be populated at Done event
					delta := &provider.Completion{
						ID:    data.Response.ID,
						Model: data.Response.Model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.TextEditorCallContent(provider.TextEditorCall{
									ID:     item.ID,
									CallID: item.CallID,
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
								ID:   event.ItemID,
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
								ID:      event.ItemID,
								Summary: event.Delta,
							}),
						},
					},
				}

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseTextDoneEvent:
			case responses.ResponseReasoningTextDoneEvent:
			case responses.ResponseReasoningSummaryPartAddedEvent:
			case responses.ResponseReasoningSummaryTextDoneEvent:
			case responses.ResponseReasoningSummaryPartDoneEvent:
			case responses.ResponseFunctionCallArgumentsDeltaEvent:
				callID := itemToCallID[event.ItemID]
				if callID == "" {
					callID = event.ItemID
				}
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Message: &provider.Message{
						Role: provider.MessageRoleAssistant,

						Content: []provider.Content{
							provider.ToolCallContent(provider.ToolCall{
								ID:        callID,
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
				switch item := event.Item.AsAny().(type) {
				case responses.ResponseApplyPatchToolCall:
					call := applyPatchToTextEditorCall(item)

					delta := &provider.Completion{
						ID:    data.Response.ID,
						Model: data.Response.Model,

						Message: &provider.Message{
							Role: provider.MessageRoleAssistant,

							Content: []provider.Content{
								provider.TextEditorCallContent(call),
							},
						},
					}

					if !yield(delta, nil) {
						return
					}

				case responses.ResponseReasoningItem:
					// Capture encrypted_content for conversation continuity
					if item.EncryptedContent != "" {
						delta := &provider.Completion{
							ID:    data.Response.ID,
							Model: data.Response.Model,

							Message: &provider.Message{
								Role: provider.MessageRoleAssistant,

								Content: []provider.Content{
									provider.ReasoningContent(provider.Reasoning{
										ID:        item.ID,
										Signature: item.EncryptedContent,
									}),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}
					}

				case responses.ResponseCompactionItem:
					if item.EncryptedContent != "" {
						delta := &provider.Completion{
							ID:    data.Response.ID,
							Model: data.Response.Model,

							Message: &provider.Message{
								Role: provider.MessageRoleAssistant,

								Content: []provider.Content{
									provider.CompactionContent(provider.Compaction{
										ID:        item.ID,
										Signature: item.EncryptedContent,
									}),
								},
							},
						}

						if !yield(delta, nil) {
							return
						}
					}
				}

			case responses.ResponseCompletedEvent:
				status := provider.CompletionStatusCompleted

				if event.Response.Status == responses.ResponseStatusIncomplete {
					status = provider.CompletionStatusIncomplete
				}

				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Status: status,
					Usage:  toResponseUsage(event.Response.Usage),
				}

				if !yield(delta, nil) {
					return
				}

			case responses.ResponseFailedEvent:
				msg := "response failed"

				if event.Response.Error.Message != "" {
					msg = event.Response.Error.Message
				}

				yield(nil, errors.New(msg))
				return

			case responses.ResponseIncompleteEvent:
				delta := &provider.Completion{
					ID:    data.Response.ID,
					Model: data.Response.Model,

					Status: provider.CompletionStatusIncomplete,
					Usage:  toResponseUsage(event.Response.Usage),
				}

				if !yield(delta, nil) {
					return
				}

			default:
				// Tolerate unknown/vendor-extension events silently
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, convertError(err))
			return
		}
	}
}

func (r *Responder) convertResponsesRequest(messages []provider.Message, options *provider.CompleteOptions) (*responses.ResponseNewParams, error) {
	if slices.Contains(ReasoningModels, r.model) && options.Temperature != nil {
		optsCopy := *options
		optsCopy.Temperature = nil
		options = &optsCopy
	}

	input, err := r.convertResponsesInput(messages)

	if err != nil {
		return nil, err
	}

	tools, err := r.convertResponsesTools(options.Tools)

	if err != nil {
		return nil, err
	}

	if options.TextEditorTool {
		tools = append(tools, responses.ToolUnionParam{
			OfApplyPatch: &responses.ApplyPatchToolParam{},
		})
	}

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

	if options.ToolOptions != nil {
		req.ToolChoice = convertResponsesToolChoice(options.ToolOptions)

		if options.ToolOptions.DisableParallelToolCalls {
			req.ParallelToolCalls = openai.Bool(false)
		}
	}

	if options.ReasoningOptions != nil && slices.Contains(ReasoningModels, r.model) {
		if options.ReasoningOptions.IncludeSignature {
			req.Include = append(req.Include, responses.ResponseIncludableReasoningEncryptedContent)
		}

		if options.ReasoningOptions.IncludeSummary {
			req.Reasoning.Summary = responses.ReasoningSummaryAuto
		}

		switch options.ReasoningOptions.Effort {
		case provider.EffortNone:
			req.Reasoning.Effort = responses.ReasoningEffortNone

		case provider.EffortMinimal:
			req.Reasoning.Effort = responses.ReasoningEffortMinimal

		case provider.EffortLow:
			req.Reasoning.Effort = responses.ReasoningEffortLow

		case provider.EffortMedium:
			req.Reasoning.Effort = responses.ReasoningEffortMedium

		case provider.EffortHigh:
			req.Reasoning.Effort = responses.ReasoningEffortHigh

		case provider.EffortMax:
			req.Reasoning.Effort = responses.ReasoningEffortXhigh
		}
	}

	if options.CompactionOptions != nil && options.CompactionOptions.Threshold > 0 {
		req.ContextManagement = []responses.ResponseNewParamsContextManagement{
			{
				Type:             "compaction",
				CompactThreshold: openai.Int(int64(options.CompactionOptions.Threshold)),
			},
		}
	}

	if options.OutputOptions != nil {
		switch options.OutputOptions.Verbosity {
		case provider.VerbosityLow:
			req.Text.Verbosity = responses.ResponseTextConfigVerbosityLow

		case provider.VerbosityMedium:
			req.Text.Verbosity = responses.ResponseTextConfigVerbosityMedium

		case provider.VerbosityHigh:
			req.Text.Verbosity = responses.ResponseTextConfigVerbosityHigh
		}
	}

	if options.Schema != nil {
		if options.Schema.Name == "json_object" {
			req.Text.Format = responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &responses.ResponseFormatJSONObjectParam{},
			}
		} else {
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
						return responses.ResponseNewParamsInputUnion{}, fmt.Errorf("unsupported content type: %s", c.File.ContentType)
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
			calls := []responses.ResponseInputItemUnionParam{}
			message := &responses.ResponseOutputMessageParam{}

			for _, c := range m.Content {
				if c.Text != "" {
					message.Content = append(message.Content, responses.ResponseOutputMessageContentUnionParam{
						OfOutputText: &responses.ResponseOutputTextParam{
							Text: c.Text,
						},
					})
				}

				if c.Reasoning != nil {
					if c.Reasoning.ID == "" {
						continue
					}

					reasoning := &responses.ResponseReasoningItemParam{
						ID: c.Reasoning.ID,
					}

					if c.Reasoning.Text != "" {
						reasoning.Content = append(reasoning.Content, responses.ResponseReasoningItemContentParam{
							Text: c.Reasoning.Text,
						})
					}

					if c.Reasoning.Summary != "" {
						reasoning.Summary = append(reasoning.Summary, responses.ResponseReasoningItemSummaryParam{
							Text: c.Reasoning.Summary,
						})
					}

					if c.Reasoning.Signature != "" {
						reasoning.EncryptedContent = openai.String(c.Reasoning.Signature)
					}

					if len(reasoning.Summary) == 0 && len(reasoning.Content) == 0 {
						reasoning.Summary = []responses.ResponseReasoningItemSummaryParam{
							{
								Text: "",
							},
						}
					}

					result = append(result, responses.ResponseInputItemUnionParam{
						OfReasoning: reasoning,
					})
				}

				if c.Compaction != nil && c.Compaction.Signature != "" {
					compaction := &responses.ResponseCompactionItemParam{
						EncryptedContent: c.Compaction.Signature,
					}

					if c.Compaction.ID != "" {
						compaction.ID = openai.String(c.Compaction.ID)
					}

					result = append(result, responses.ResponseInputItemUnionParam{
						OfCompaction: compaction,
					})
				}

				if c.ToolCall != nil {
					call := &responses.ResponseFunctionToolCallParam{
						CallID: c.ToolCall.ID,

						Name:      c.ToolCall.Name,
						Arguments: c.ToolCall.Arguments,
					}

					calls = append(calls, responses.ResponseInputItemUnionParam{
						OfFunctionCall: call,
					})
				}
			}

			if len(message.Content) > 0 {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfOutputMessage: message,
				})
			}

			if len(calls) > 0 {
				result = append(result, calls...)
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

func applyPatchToTextEditorCall(item responses.ResponseApplyPatchToolCall) provider.TextEditorCall {
	call := provider.TextEditorCall{
		ID:     item.ID,
		CallID: item.CallID,
	}

	switch op := item.Operation.AsAny().(type) {
	case responses.ResponseApplyPatchToolCallOperationCreateFile:
		call.Command = provider.TextEditorCommandCreate
		call.Path = op.Path
		call.Content = parseDiffContent(op.Diff)
	case responses.ResponseApplyPatchToolCallOperationUpdateFile:
		call.Command = provider.TextEditorCommandStrReplace
		call.Path = op.Path
		call.Content = parseDiffContent(op.Diff)
	case responses.ResponseApplyPatchToolCallOperationDeleteFile:
		call.Path = op.Path
	}

	return call
}

// parseDiffContent extracts the added lines from a diff string.
func parseDiffContent(diff string) string {
	var lines []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") {
			lines = append(lines, strings.TrimPrefix(line, "+"))
		}
	}
	return strings.Join(lines, "\n")
}

func toResponseUsage(usage responses.ResponseUsage) *provider.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),

		CacheReadInputTokens: int(usage.InputTokensDetails.CachedTokens),
	}
}

func convertResponsesToolChoice(opts *provider.ToolOptions) responses.ResponseNewParamsToolChoiceUnion {
	if len(opts.Allowed) == 0 {
		modes := map[provider.ToolChoice]responses.ToolChoiceOptions{
			provider.ToolChoiceNone: responses.ToolChoiceOptionsNone,
			provider.ToolChoiceAuto: responses.ToolChoiceOptionsAuto,
			provider.ToolChoiceAny:  responses.ToolChoiceOptionsRequired,
		}

		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(modes[opts.Choice]),
		}
	}

	var tools []map[string]any

	for _, name := range opts.Allowed {
		tools = append(tools, map[string]any{"type": "function", "name": name})
	}

	mode := responses.ToolChoiceAllowedModeRequired

	if opts.Choice == provider.ToolChoiceAuto {
		mode = responses.ToolChoiceAllowedModeAuto
	}

	return responses.ResponseNewParamsToolChoiceUnion{
		OfAllowedTools: &responses.ToolChoiceAllowedParam{
			Mode:  mode,
			Tools: tools,
		},
	}
}
