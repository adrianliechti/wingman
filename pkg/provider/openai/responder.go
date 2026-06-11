package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"iter"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/computeruse"
	"github.com/adrianliechti/wingman/pkg/provider/tools/shell"
	"github.com/adrianliechti/wingman/pkg/provider/tools/texteditor"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
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
		seenCompactions := make(map[string]struct{})

		var responseID, responseModel string

		emit := func(content provider.Content, status provider.CompletionStatus) bool {
			return yield(&provider.Completion{
				ID:     responseID,
				Model:  responseModel,
				Status: status,

				Message: &provider.Message{
					Role:    provider.MessageRoleAssistant,
					Content: []provider.Content{content},
				},
			}, nil)
		}

		emitStatus := func(status provider.CompletionStatus, usage *provider.Usage) bool {
			return yield(&provider.Completion{
				ID:     responseID,
				Model:  responseModel,
				Status: status,
				Usage:  usage,
			}, nil)
		}

		yieldCompaction := func(item responses.ResponseCompactionItem) bool {
			if item.EncryptedContent == "" {
				return true
			}

			key := item.ID
			if key == "" {
				key = item.EncryptedContent
			}

			if _, ok := seenCompactions[key]; ok {
				return true
			}

			seenCompactions[key] = struct{}{}

			return emit(provider.CompactionContent(provider.Compaction{
				ID:        item.ID,
				Signature: item.EncryptedContent,
			}), "")
		}

		for stream.Next() {
			data := stream.Current()
			responseID, responseModel = data.Response.ID, data.Response.Model

			switch event := data.AsAny().(type) {
			case responses.ResponseCreatedEvent:
			case responses.ResponseInProgressEvent:
			case responses.ResponseOutputItemAddedEvent:
				switch item := event.Item.AsAny().(type) {
				case responses.ResponseFunctionToolCall:
					itemToCallID[item.ID] = item.CallID
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Name: item.Name, Namespace: item.Namespace}), "") {
						return
					}

				case responses.ResponseCustomToolCall:
					itemToCallID[item.ID] = item.CallID
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindCustom, Name: item.Name}), "") {
						return
					}

				case responses.ResponseApplyPatchToolCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindTextEditor, Name: "apply_patch"}), "") {
						return
					}

				case responses.ResponseComputerToolCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindComputer, Name: "computer"}), "") {
						return
					}

				case responses.ResponseFunctionShellToolCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameShell}), "") {
						return
					}

				case responses.ResponseOutputItemLocalShellCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameLocalShell}), "") {
						return
					}
				}

			case responses.ResponseContentPartAddedEvent:
			case responses.ResponseTextDeltaEvent:
				if !emit(provider.TextContent(event.Delta), "") {
					return
				}

			case responses.ResponseRefusalDeltaEvent:
				if !emit(provider.RefusalContent(event.Delta), provider.CompletionStatusRefused) {
					return
				}

			case responses.ResponseReasoningTextDeltaEvent:
				if !emit(provider.ReasoningContent(provider.Reasoning{ID: event.ItemID, Text: event.Delta}), "") {
					return
				}

			case responses.ResponseReasoningSummaryTextDeltaEvent:
				if !emit(provider.ReasoningContent(provider.Reasoning{ID: event.ItemID, Summary: event.Delta}), "") {
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
				if !emit(provider.ToolCallContent(provider.ToolCall{ID: callID, Arguments: event.Delta}), "") {
					return
				}

			case responses.ResponseCustomToolCallInputDeltaEvent:
				callID := itemToCallID[event.ItemID]
				if callID == "" {
					callID = event.ItemID
				}
				if !emit(provider.ToolCallContent(provider.ToolCall{ID: callID, Arguments: event.Delta}), "") {
					return
				}

			case responses.ResponseFunctionCallArgumentsDoneEvent:
			case responses.ResponseCustomToolCallInputDoneEvent:
			case responses.ResponseContentPartDoneEvent:
			case responses.ResponseOutputItemDoneEvent:
				switch item := event.Item.AsAny().(type) {
				case responses.ResponseApplyPatchToolCall:
					args, _ := json.Marshal(map[string]any{
						"type": item.Operation.Type,
						"path": item.Operation.Path,
						"diff": item.Operation.Diff,
					})
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindTextEditor, Name: "apply_patch", Arguments: string(args)}), "") {
						return
					}

				case responses.ResponseComputerToolCall:
					args, _ := json.Marshal(computerCallToArgs(item))
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindComputer, Name: "computer", Arguments: string(args)}), "") {
						return
					}

				case responses.ResponseFunctionShellToolCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameShell, Arguments: item.Action.RawJSON()}), "") {
						return
					}

				case responses.ResponseOutputItemLocalShellCall:
					if !emit(provider.ToolCallContent(provider.ToolCall{ID: item.CallID, Kind: provider.ToolKindShell, Name: shell.NameLocalShell, Arguments: item.Action.RawJSON()}), "") {
						return
					}

				case responses.ResponseToolSearchCall:
					args, _ := json.Marshal(item.Arguments)
					if !emit(provider.ToolCallContent(provider.ToolCall{
						ID:        item.CallID,
						Kind:      provider.ToolKindToolSearch,
						Name:      "tool_search",
						Execution: string(item.Execution),
						Arguments: string(args),
					}), "") {
						return
					}

				case responses.ResponseReasoningItem:
					// Capture encrypted_content for conversation continuity
					if item.ID != "" || item.EncryptedContent != "" {
						if !emit(provider.ReasoningContent(provider.Reasoning{ID: item.ID, Signature: item.EncryptedContent}), "") {
							return
						}
					}

				case responses.ResponseCompactionItem:
					if !yieldCompaction(item) {
						return
					}
				}

			case responses.ResponseCompletedEvent:
				for _, output := range event.Response.Output {
					if item, ok := output.AsAny().(responses.ResponseCompactionItem); ok {
						if !yieldCompaction(item) {
							return
						}
					}
				}

				status := provider.CompletionStatusCompleted

				if event.Response.Status == responses.ResponseStatusIncomplete {
					status = provider.CompletionStatusIncomplete
				}

				if !emitStatus(status, toResponseUsage(event.Response.Usage)) {
					return
				}

			case responses.ResponseFailedEvent:
				errCode := string(event.Response.Error.Code)

				msg := event.Response.Error.Message
				if msg == "" {
					if errCode != "" {
						msg = "response failed: " + errCode
					} else {
						msg = "response failed"
					}
				}

				yield(nil, &provider.ProviderError{
					Code:    statusCodeFromResponseErrorCode(errCode),
					Type:    errCode,
					Message: msg,
				})
				return

			case responses.ResponseIncompleteEvent:
				if !emitStatus(provider.CompletionStatusIncomplete, toResponseUsage(event.Response.Usage)) {
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
	if !isLegacyModel(r.model) && options.Temperature != nil {
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

	req := &responses.ResponseNewParams{
		Model: r.model,

		Store: openai.Bool(false),

		Input: input,
		Tools: tools,

		Truncation: responses.ResponseNewParamsTruncationAuto,
	}

	if options.ToolOptions != nil {
		req.ToolChoice = convertResponsesToolChoice(options.ToolOptions)

		if options.ToolOptions.DisableParallelToolCalls {
			req.ParallelToolCalls = openai.Bool(false)
		}
	}

	if options.ReasoningOptions != nil && !isLegacyModel(r.model) {
		reasoning := options.ReasoningOptions

		if reasoning.IncludeSignature {
			req.Include = append(req.Include, responses.ResponseIncludableReasoningEncryptedContent)
		}

		if reasoning.IncludeSummary {
			req.Reasoning.Summary = responses.ReasoningSummaryAuto
		}

		switch reasoning.Effort {
		case provider.EffortMinimal:
			req.Reasoning.Effort = responses.ReasoningEffortMinimal

		case provider.EffortLow:
			req.Reasoning.Effort = responses.ReasoningEffortLow

		case provider.EffortMedium:
			req.Reasoning.Effort = responses.ReasoningEffortMedium

		case provider.EffortHigh:
			req.Reasoning.Effort = responses.ReasoningEffortHigh

		case provider.EffortXHigh, provider.EffortMax:
			req.Reasoning.Effort = responses.ReasoningEffortXhigh

		default:
			if reasoning.Type == provider.ReasoningTypeAdaptive {
				req.Reasoning.Effort = responses.ReasoningEffortMedium
			}
		}

		if reasoning.Type == provider.ReasoningTypeDisabled {
			req.Reasoning.Effort = responses.ReasoningEffortNone
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
			properties := options.Schema.Properties

			if options.Schema.Strict != nil && *options.Schema.Strict {
				properties = ensureAdditionalPropertiesFalse(properties)
			}

			schema := &responses.ResponseFormatTextJSONSchemaConfigParam{
				Name:   options.Schema.Name,
				Schema: properties,
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

			if !isLegacyModel(r.model) {
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
					url := "data:" + c.File.ContentType + ";base64," + base64.StdEncoding.EncodeToString(c.File.Content)

					switch c.File.ContentType {
					case "image/png", "image/jpeg", "image/webp", "image/gif":
						message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(url),
							},
						})

					default:
						// Forward as a generic input_file — OpenAI Responses' wire
						// accepts any mime here and the model decides what it can
						// interpret. Matches the tool-result side's behavior.
						name := c.File.Name
						if name == "" {
							name = "file"
						}

						message.Content = append(message.Content, responses.ResponseInputContentUnionParam{
							OfInputFile: &responses.ResponseInputFileParam{
								Filename: openai.String(name),
								FileData: openai.String(url),
							},
						})
					}
				}

				if c.ToolResult != nil {
					switch c.ToolResult.Kind {
					case provider.ToolKindTextEditor:
						if !r.isOpenAI() {
							// emulated function tool — replay as function_call_output
							result = append(result, responses.ResponseInputItemUnionParam{
								OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
									CallID: c.ToolResult.ID,
									Output: toolResultOutputUnion(c.ToolResult),
								},
							})
							continue
						}

						output := &responses.ResponseInputItemApplyPatchCallOutputParam{
							CallID: c.ToolResult.ID,
							Status: "completed",
						}

						var texts []string
						for _, p := range c.ToolResult.Parts {
							if p.Text != "" {
								texts = append(texts, p.Text)
							}
						}

						if len(texts) > 0 {
							output.Output = openai.String(strings.Join(texts, "\n"))
						}

						result = append(result, responses.ResponseInputItemUnionParam{
							OfApplyPatchCallOutput: output,
						})

					case provider.ToolKindComputer:
						if !r.isOpenAI() {
							// emulated function tool — replay as function_call_output
							result = append(result, responses.ResponseInputItemUnionParam{
								OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
									CallID: c.ToolResult.ID,
									Output: toolResultOutputUnion(c.ToolResult),
								},
							})
							continue
						}

						result = append(result, responses.ResponseInputItemUnionParam{
							OfComputerCallOutput: computerCallOutputParam(c.ToolResult),
						})

					case provider.ToolKindShell:
						if item, ok := shellCallOutputParam(c.ToolResult, r.isOpenAI()); ok {
							result = append(result, item)
						} else {
							// emulated function tool — replay as function_call_output
							result = append(result, responses.ResponseInputItemUnionParam{
								OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
									CallID: c.ToolResult.ID,
									Output: toolResultOutputUnion(c.ToolResult),
								},
							})
						}

					case provider.ToolKindCustom:
						result = append(result, responses.ResponseInputItemUnionParam{
							OfCustomToolCallOutput: &responses.ResponseCustomToolCallOutputParam{
								CallID: c.ToolResult.ID,
								Output: customToolResultOutputUnion(c.ToolResult),
							},
						})

					case provider.ToolKindToolSearch:
						tso := &responses.ResponseToolSearchOutputItemParam{
							Status: responses.ResponseToolSearchOutputItemParamStatusCompleted,
						}
						if c.ToolResult.ID != "" {
							tso.CallID = openai.String(c.ToolResult.ID)
						}
						if c.ToolResult.Execution != "" {
							tso.Execution = responses.ResponseToolSearchOutputItemParamExecution(c.ToolResult.Execution)
						}
						if len(c.ToolResult.Payload) > 0 {
							var raw []responses.ToolUnionParam
							if err := json.Unmarshal(c.ToolResult.Payload, &raw); err == nil {
								tso.Tools = raw
							}
						}
						result = append(result, responses.ResponseInputItemUnionParam{
							OfToolSearchOutput: tso,
						})

					default:
						result = append(result, responses.ResponseInputItemUnionParam{
							OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
								CallID: c.ToolResult.ID,
								Output: toolResultOutputUnion(c.ToolResult),
							},
						})
					}
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

					// `summary` is required by the API; an empty array preserves the
					// original structure. A placeholder part would break encrypted_content.
					if reasoning.Summary == nil {
						reasoning.Summary = []responses.ResponseReasoningItemSummaryParam{}
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
					switch c.ToolCall.Kind {
					case provider.ToolKindTextEditor:
						if c.ToolCall.Name == texteditor.NameTextEditor || !r.isOpenAI() {
							// emulated function tool — replay as function_call
							calls = append(calls, responses.ResponseInputItemUnionParam{
								OfFunctionCall: &responses.ResponseFunctionToolCallParam{
									CallID:    c.ToolCall.ID,
									Name:      c.ToolCall.Name,
									Arguments: c.ToolCall.Arguments,
								},
							})
							continue
						}

						calls = append(calls, responses.ResponseInputItemUnionParam{
							OfApplyPatchCall: &responses.ResponseInputItemApplyPatchCallParam{
								CallID:    c.ToolCall.ID,
								Status:    "completed",
								Operation: applyPatchOperationUnion(texteditor.ParseOperation(c.ToolCall.Arguments)),
							},
						})

					case provider.ToolKindComputer:
						if !r.isOpenAI() {
							// emulated function tool — replay as function_call
							calls = append(calls, responses.ResponseInputItemUnionParam{
								OfFunctionCall: &responses.ResponseFunctionToolCallParam{
									CallID:    c.ToolCall.ID,
									Name:      c.ToolCall.Name,
									Arguments: c.ToolCall.Arguments,
								},
							})
							continue
						}

						item := computerCallParam(*c.ToolCall)
						calls = append(calls, responses.ResponseInputItemUnionParam{
							OfComputerCall: &item,
						})

					case provider.ToolKindShell:
						if item, ok := shellCallParam(*c.ToolCall, r.isOpenAI()); ok {
							calls = append(calls, item)
						} else {
							// emulated function tool — replay as function_call
							calls = append(calls, responses.ResponseInputItemUnionParam{
								OfFunctionCall: &responses.ResponseFunctionToolCallParam{
									CallID:    c.ToolCall.ID,
									Name:      c.ToolCall.Name,
									Arguments: c.ToolCall.Arguments,
								},
							})
						}

					case provider.ToolKindCustom:
						calls = append(calls, responses.ResponseInputItemUnionParam{
							OfCustomToolCall: &responses.ResponseCustomToolCallParam{
								CallID: c.ToolCall.ID,
								Name:   c.ToolCall.Name,
								Input:  c.ToolCall.Arguments,
							},
						})

					case provider.ToolKindToolSearch:
						ts := &responses.ResponseInputItemToolSearchCallParam{
							Status: "completed",
						}
						if c.ToolCall.ID != "" {
							ts.CallID = openai.String(c.ToolCall.ID)
						}
						if c.ToolCall.Execution != "" {
							ts.Execution = c.ToolCall.Execution
						}
						if c.ToolCall.Arguments != "" {
							var args any
							if err := json.Unmarshal([]byte(c.ToolCall.Arguments), &args); err == nil {
								ts.Arguments = args
							} else {
								ts.Arguments = c.ToolCall.Arguments
							}
						}
						calls = append(calls, responses.ResponseInputItemUnionParam{
							OfToolSearchCall: ts,
						})

					default:
						fc := &responses.ResponseFunctionToolCallParam{
							CallID:    c.ToolCall.ID,
							Name:      c.ToolCall.Name,
							Arguments: c.ToolCall.Arguments,
						}
						if c.ToolCall.Namespace != "" {
							fc.Namespace = openai.String(c.ToolCall.Namespace)
						}
						calls = append(calls, responses.ResponseInputItemUnionParam{
							OfFunctionCall: fc,
						})
					}
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

// toolResultOutputUnion maps provider.ToolResult.Parts to the OpenAI Responses
// function_call_output union: a plain string when there is only a single text
// part, or an array of input_text / input_image / input_file otherwise.
func toolResultOutputUnion(r *provider.ToolResult) responses.ResponseInputItemFunctionCallOutputOutputUnionParam {
	if len(r.Parts) == 0 {
		return responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
			OfString: openai.String(""),
		}
	}

	if len(r.Parts) == 1 && r.Parts[0].File == nil {
		return responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
			OfString: openai.String(r.Parts[0].Text),
		}
	}

	items := make(responses.ResponseFunctionCallOutputItemListParam, 0, len(r.Parts))

	for _, p := range r.Parts {
		if p.Text != "" {
			items = append(items, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputText: &responses.ResponseInputTextContentParam{
					Text: p.Text,
				},
			})
		}

		if p.File != nil {
			url := "data:" + p.File.ContentType + ";base64," + base64.StdEncoding.EncodeToString(p.File.Content)

			switch p.File.ContentType {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
				items = append(items, responses.ResponseFunctionCallOutputItemUnionParam{
					OfInputImage: &responses.ResponseInputImageContentParam{
						ImageURL: openai.String(url),
					},
				})

			default:
				name := p.File.Name
				if name == "" {
					name = "file"
				}

				items = append(items, responses.ResponseFunctionCallOutputItemUnionParam{
					OfInputFile: &responses.ResponseInputFileContentParam{
						Filename: openai.String(name),
						FileData: openai.String(url),
					},
				})
			}
		}
	}

	return responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
		OfResponseFunctionCallOutputItemArray: items,
	}
}

func customToolResultOutputUnion(r *provider.ToolResult) responses.ResponseCustomToolCallOutputOutputUnionParam {
	if len(r.Parts) == 0 {
		return responses.ResponseCustomToolCallOutputOutputUnionParam{
			OfString: openai.String(""),
		}
	}

	if len(r.Parts) == 1 && r.Parts[0].File == nil {
		return responses.ResponseCustomToolCallOutputOutputUnionParam{
			OfString: openai.String(r.Parts[0].Text),
		}
	}

	items := make([]responses.ResponseCustomToolCallOutputOutputOutputContentListItemUnionParam, 0, len(r.Parts))

	for _, p := range r.Parts {
		if p.Text != "" {
			items = append(items, responses.ResponseCustomToolCallOutputOutputOutputContentListItemUnionParam{
				OfInputText: &responses.ResponseInputTextParam{
					Text: p.Text,
				},
			})
		}

		if p.File != nil {
			url := "data:" + p.File.ContentType + ";base64," + base64.StdEncoding.EncodeToString(p.File.Content)

			switch p.File.ContentType {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
				items = append(items, responses.ResponseCustomToolCallOutputOutputOutputContentListItemUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						ImageURL: openai.String(url),
					},
				})

			default:
				name := p.File.Name
				if name == "" {
					name = "file"
				}

				items = append(items, responses.ResponseCustomToolCallOutputOutputOutputContentListItemUnionParam{
					OfInputFile: &responses.ResponseInputFileParam{
						Filename: openai.String(name),
						FileData: openai.String(url),
					},
				})
			}
		}
	}

	return responses.ResponseCustomToolCallOutputOutputUnionParam{
		OfOutputContentList: items,
	}
}

func (r *Responder) convertResponsesTools(tools []provider.Tool) ([]responses.ToolUnionParam, error) {
	var result []responses.ToolUnionParam

	for _, t := range tools {
		if t.Kind == provider.ToolKindTextEditor {
			if t.Name != texteditor.NameTextEditor && r.isOpenAI() {
				result = append(result, responses.ToolUnionParam{
					OfApplyPatch: &responses.ApplyPatchToolParam{},
				})
				continue
			}

			// Anthropic dialect, or a host without the native apply_patch tool —
			// emulate as a function tool so calls and results stay in the
			// client's dialect end-to-end
			t = texteditor.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindComputer {
			if t.Dialect != computeruse.DialectAnthropic && r.isOpenAI() {
				result = append(result, responses.ToolUnionParam{
					OfComputer: &responses.ComputerToolParam{},
				})
				continue
			}

			// Anthropic dialect, or a host without the native computer tool —
			// emulate as a function tool in the client's dialect
			t = computeruse.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindShell {
			if r.isOpenAI() && t.Name == shell.NameShell {
				result = append(result, responses.ToolUnionParam{
					OfShell: &responses.FunctionShellToolParam{},
				})
				continue
			}

			if r.isOpenAI() && t.Name == shell.NameLocalShell {
				result = append(result, responses.ToolUnionParam{
					OfLocalShell: &responses.ToolLocalShellParam{},
				})
				continue
			}

			// bash dialect, or a host without the native shell tools — emulate
			// as a function tool in the client's dialect
			t = shell.FunctionTool(t)
		}

		if t.Kind == provider.ToolKindToolSearch {
			search := &responses.ToolSearchToolParam{}
			if t.Description != "" {
				search.Description = openai.String(t.Description)
			}
			if t.Execution != "" {
				search.Execution = responses.ToolSearchToolExecution(t.Execution)
			}
			if len(t.Parameters) > 0 {
				search.Parameters = t.Parameters
			}
			result = append(result, responses.ToolUnionParam{
				OfToolSearch: search,
			})
			continue
		}

		if len(t.Tools) > 0 {
			ns := &responses.NamespaceToolParam{
				Name:        t.Name,
				Description: t.Description,
			}
			for _, inner := range t.Tools {
				if inner.Kind == provider.ToolKindCustom || inner.Name == "" {
					continue
				}
				fn := responses.NamespaceToolToolFunctionParam{
					Name:       inner.Name,
					Parameters: inner.Parameters,
				}
				if inner.Description != "" {
					fn.Description = openai.String(inner.Description)
				}
				if inner.Strict != nil {
					fn.Strict = openai.Bool(*inner.Strict)
				}
				if inner.Deferred != nil && *inner.Deferred {
					fn.DeferLoading = openai.Bool(true)
				}
				ns.Tools = append(ns.Tools, responses.NamespaceToolToolUnionParam{
					OfFunction: &fn,
				})
			}
			if len(ns.Tools) > 0 {
				result = append(result, responses.ToolUnionParam{
					OfNamespace: ns,
				})
			}
			continue
		}

		if t.Name == "" {
			continue
		}

		if t.Kind == provider.ToolKindCustom {
			custom := &responses.CustomToolParam{
				Name: t.Name,
			}

			if t.Description != "" {
				custom.Description = openai.String(t.Description)
			}

			if t.Format != nil && t.Format.Type == "grammar" && t.Format.Definition != "" {
				custom.Format = shared.CustomToolInputFormatParamOfGrammar(t.Format.Definition, t.Format.Syntax)
			}

			if t.Deferred != nil && *t.Deferred {
				custom.DeferLoading = openai.Bool(true)
			}

			result = append(result, responses.ToolUnionParam{
				OfCustom: custom,
			})
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

		if t.Deferred != nil && *t.Deferred {
			function.DeferLoading = openai.Bool(true)
		}

		result = append(result, responses.ToolUnionParam{
			OfFunction: function,
		})
	}

	return result, nil
}

func applyPatchOperationUnion(op texteditor.Operation) responses.ResponseInputItemApplyPatchCallOperationUnionParam {
	switch op.Type {
	case "create_file":
		return responses.ResponseInputItemApplyPatchCallOperationUnionParam{
			OfCreateFile: &responses.ResponseInputItemApplyPatchCallOperationCreateFileParam{
				Path: op.Path,
				Diff: op.Diff,
			},
		}

	case "delete_file":
		return responses.ResponseInputItemApplyPatchCallOperationUnionParam{
			OfDeleteFile: &responses.ResponseInputItemApplyPatchCallOperationDeleteFileParam{
				Path: op.Path,
			},
		}

	default:
		return responses.ResponseInputItemApplyPatchCallOperationUnionParam{
			OfUpdateFile: &responses.ResponseInputItemApplyPatchCallOperationUpdateFileParam{
				Path: op.Path,
				Diff: op.Diff,
			},
		}
	}
}

// computerCallParam rebuilds a computer_call input item from the canonical
// {call_id, actions, pending_safety_checks} arguments. The SDK only offers
// typed per-action unions, so the item is injected as raw JSON.
func computerCallParam(call provider.ToolCall) responses.ResponseComputerToolCallParam {
	c := computeruse.ParseCall(call.Arguments)

	actions := c.Actions
	if actions == nil {
		actions = []map[string]any{}
	}

	checks := c.PendingSafetyChecks
	if checks == nil {
		checks = []computeruse.SafetyCheck{}
	}

	data, _ := json.Marshal(map[string]any{
		"type":    "computer_call",
		"call_id": call.ID,
		"status":  "completed",
		"actions": actions,

		"pending_safety_checks": checks,
	})

	return param.Override[responses.ResponseComputerToolCallParam](json.RawMessage(data))
}

func computerCallOutputParam(r *provider.ToolResult) *responses.ResponseInputItemComputerCallOutputParam {
	output := &responses.ResponseInputItemComputerCallOutputParam{
		CallID: r.ID,
	}

	for _, p := range r.Parts {
		if p.File != nil {
			url := "data:" + p.File.ContentType + ";base64," + base64.StdEncoding.EncodeToString(p.File.Content)
			output.Output.ImageURL = openai.String(url)
			break
		}

		// file_id screenshots round-trip as their JSON payload (see
		// computerOutputParts in the responses server)
		var screenshot struct {
			Type     string `json:"type"`
			ImageURL string `json:"image_url"`
			FileID   string `json:"file_id"`
		}

		if err := json.Unmarshal([]byte(p.Text), &screenshot); err == nil && screenshot.Type == "computer_screenshot" {
			if screenshot.ImageURL != "" {
				output.Output.ImageURL = openai.String(screenshot.ImageURL)
			}
			if screenshot.FileID != "" {
				output.Output.FileID = openai.String(screenshot.FileID)
			}
			break
		}
	}

	if len(r.Payload) > 0 {
		var checks []computeruse.SafetyCheck

		if err := json.Unmarshal(r.Payload, &checks); err == nil {
			for _, c := range checks {
				ack := responses.ResponseInputItemComputerCallOutputAcknowledgedSafetyCheckParam{
					ID: c.ID,
				}

				if c.Code != "" {
					ack.Code = openai.String(c.Code)
				}

				if c.Message != "" {
					ack.Message = openai.String(c.Message)
				}

				output.AcknowledgedSafetyChecks = append(output.AcknowledgedSafetyChecks, ack)
			}
		}
	}

	return output
}

// shellCallParam builds the native replay item for a shell call. ok is false
// when the tool runs emulated (bash dialect or non-OpenAI host) and the call
// must replay as a function_call instead.
func shellCallParam(call provider.ToolCall, native bool) (responses.ResponseInputItemUnionParam, bool) {
	if !native {
		return responses.ResponseInputItemUnionParam{}, false
	}

	switch call.Name {
	case shell.NameShell:
		data, _ := json.Marshal(map[string]any{
			"type":    "shell_call",
			"call_id": call.ID,
			"status":  "completed",
			"action":  shell.ShellAction(call.Arguments),
		})

		item := param.Override[responses.ResponseInputItemShellCallParam](json.RawMessage(data))
		return responses.ResponseInputItemUnionParam{OfShellCall: &item}, true

	case shell.NameLocalShell:
		action := shell.LocalShellAction(call.Arguments)

		if _, ok := action["env"]; !ok {
			action["env"] = map[string]string{}
		}

		data, _ := json.Marshal(map[string]any{
			"type":    "local_shell_call",
			"id":      "lsc_" + call.ID,
			"call_id": call.ID,
			"status":  "completed",
			"action":  action,
		})

		item := param.Override[responses.ResponseInputItemLocalShellCallParam](json.RawMessage(data))
		return responses.ResponseInputItemUnionParam{OfLocalShellCall: &item}, true
	}

	return responses.ResponseInputItemUnionParam{}, false
}

// shellCallOutputParam rebuilds the native output item from the replay
// envelope the responses server stores in ToolResult.Payload.
func shellCallOutputParam(r *provider.ToolResult, native bool) (responses.ResponseInputItemUnionParam, bool) {
	if !native {
		return responses.ResponseInputItemUnionParam{}, false
	}

	var envelope struct {
		Type   string          `json:"type"`
		Output json.RawMessage `json:"output"`
	}

	json.Unmarshal(r.Payload, &envelope)

	if envelope.Type == "local_shell_call_output" {
		var output string
		json.Unmarshal(envelope.Output, &output)

		return responses.ResponseInputItemUnionParam{
			OfLocalShellCallOutput: &responses.ResponseInputItemLocalShellCallOutputParam{
				ID:     r.ID,
				Output: output,
			},
		}, true
	}

	output := envelope.Output

	if len(output) == 0 {
		var text strings.Builder
		for _, p := range r.Parts {
			text.WriteString(p.Text)
		}

		output, _ = json.Marshal([]map[string]any{
			{
				"stdout":  text.String(),
				"outcome": map[string]any{"type": "exit", "exit_code": 0},
			},
		})
	}

	data, _ := json.Marshal(map[string]any{
		"type":    "shell_call_output",
		"call_id": r.ID,
		"output":  output,
	})

	item := param.Override[responses.ResponseInputItemShellCallOutputParam](json.RawMessage(data))
	return responses.ResponseInputItemUnionParam{OfShellCallOutput: &item}, true
}

func computerCallToArgs(item responses.ResponseComputerToolCall) map[string]any {
	result := map[string]any{
		"call_id": item.CallID,
	}

	if len(item.PendingSafetyChecks) > 0 {
		var checks []map[string]any
		for _, c := range item.PendingSafetyChecks {
			checks = append(checks, map[string]any{"id": c.ID, "code": c.Code, "message": c.Message})
		}
		result["pending_safety_checks"] = checks
	}

	if len(item.Actions) > 0 {
		var actions []map[string]any
		for _, a := range item.Actions {
			action := map[string]any{"type": a.Type}

			switch v := a.AsAny().(type) {
			case responses.ComputerActionClick:
				action["x"] = v.X
				action["y"] = v.Y
				action["button"] = v.Button
			case responses.ComputerActionDoubleClick:
				action["x"] = v.X
				action["y"] = v.Y
			case responses.ComputerActionMove:
				action["x"] = v.X
				action["y"] = v.Y
			case responses.ComputerActionType:
				action["text"] = v.Text
			case responses.ComputerActionKeypress:
				action["keys"] = v.Keys
			case responses.ComputerActionScroll:
				action["x"] = v.X
				action["y"] = v.Y
				action["scroll_x"] = v.ScrollX
				action["scroll_y"] = v.ScrollY
			case responses.ComputerActionDrag:
				if len(v.Path) > 0 {
					var path []map[string]any
					for _, p := range v.Path {
						path = append(path, map[string]any{"x": p.X, "y": p.Y})
					}
					action["path"] = path
				}
			}

			actions = append(actions, action)
		}
		result["actions"] = actions
	}

	return result
}

func toResponseUsage(usage responses.ResponseUsage) *provider.Usage {
	if usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.InputTokensDetails.CachedTokens == 0 {
		return nil
	}

	return &provider.Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),

		ReasoningTokens: int(usage.OutputTokensDetails.ReasoningTokens),

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
