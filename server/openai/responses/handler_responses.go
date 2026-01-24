package responses

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/google/uuid"
)

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req ResponsesRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Input.Items, req.Instructions)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.CompleteOptions{
		Tools: tools,

		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
	}

	if req.Reasoning != nil && req.Reasoning.Effort != nil {
		switch *req.Reasoning.Effort {
		case ReasoningEffortMinimal:
			options.Effort = provider.EffortMinimal

		case ReasoningEffortLow:
			options.Effort = provider.EffortLow

		case ReasoningEffortMedium:
			options.Effort = provider.EffortMedium

		case ReasoningEffortHigh, ReasoningEffortXHigh:
			options.Effort = provider.EffortHigh
		}
	}

	// Handle structured output configuration
	if req.Text != nil {
		if req.Text.Format != nil {
			if req.Text.Format.Type == "json_object" {
				options.Schema = &provider.Schema{
					Name: "json_object",
				}
			}

			if req.Text.Format.Type == "json_schema" && req.Text.Format.Schema != nil {
				options.Schema = &provider.Schema{
					Name:        req.Text.Format.Name,
					Description: req.Text.Format.Description,

					Schema: req.Text.Format.Schema,
					Strict: req.Text.Format.Strict,
				}
			}
		}

		if req.Text.Verbosity != nil {
			switch *req.Text.Verbosity {
			case VerbosityLow:
				options.Verbosity = provider.VerbosityLow

			case VerbosityMedium:
				options.Verbosity = provider.VerbosityMedium

			case VerbosityHigh:
				options.Verbosity = provider.VerbosityHigh
			}
		}
	}

	if req.Stream {
		h.handleResponsesStream(w, r, req, completer, messages, options)
	} else {
		h.handleResponsesComplete(w, r, req, completer, messages, options)
	}
}

func (h *Handler) handleResponsesStream(w http.ResponseWriter, r *http.Request, req ResponsesRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	createdAt := time.Now().Unix()

	responseID := "resp_" + uuid.NewString()
	messageID := "msg_" + uuid.NewString()

	seqNum := 0

	// Helper to get sequence number and increment
	nextSeq := func() int {
		n := seqNum
		seqNum++
		return n
	}

	// Create initial response template
	createResponse := func(status string, output []ResponseOutput) *Response {
		return &Response{
			ID:        responseID,
			Object:    "response",
			CreatedAt: createdAt,
			Status:    status,
			Model:     req.Model,
			Output:    output,
		}
	}

	// Create streaming accumulator with event handler
	accumulator := NewStreamingAccumulator(func(event StreamEvent) error {
		switch event.Type {
		case StreamEventResponseCreated:
			return writeEvent(w, "response.created", ResponseCreatedEvent{
				Type:           "response.created",
				SequenceNumber: nextSeq(),
				Response:       createResponse("in_progress", []ResponseOutput{}),
			})

		case StreamEventResponseInProgress:
			return writeEvent(w, "response.in_progress", ResponseInProgressEvent{
				Type:           "response.in_progress",
				SequenceNumber: nextSeq(),
				Response:       createResponse("in_progress", []ResponseOutput{}),
			})

		case StreamEventOutputItemAdded:
			return writeEvent(w, "response.output_item.added", OutputItemAddedEvent{
				Type:           "response.output_item.added",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &OutputItem{
					ID:      messageID,
					Type:    "message",
					Status:  "in_progress",
					Content: []OutputContent{},
					Role:    MessageRoleAssistant,
				},
			})

		case StreamEventContentPartAdded:
			return writeEvent(w, "response.content_part.added", ContentPartAddedEvent{
				Type:           "response.content_part.added",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Part: &OutputContent{
					Type: "output_text",
					Text: "",
				},
			})

		case StreamEventTextDelta:
			return writeEvent(w, "response.output_text.delta", OutputTextDeltaEvent{
				Type:           "response.output_text.delta",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Delta:          event.Delta,
			})

		case StreamEventTextDone:
			return writeEvent(w, "response.output_text.done", OutputTextDoneEvent{
				Type:           "response.output_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Text:           event.Text,
			})

		case StreamEventContentPartDone:
			return writeEvent(w, "response.content_part.done", ContentPartDoneEvent{
				Type:           "response.content_part.done",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Part: &OutputContent{
					Type: "output_text",
					Text: event.Text,
				},
			})

		case StreamEventFunctionCallAdded:
			return writeEvent(w, "response.output_item.added", FunctionCallOutputItemAddedEvent{
				Type:           "response.output_item.added",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &FunctionCallOutputItem{
					ID:        event.ToolCallID,
					Type:      "function_call",
					Status:    "in_progress",
					CallID:    event.ToolCallID,
					Name:      event.ToolCallName,
					Arguments: "",
				},
			})

		case StreamEventFunctionCallArgumentsDelta:
			return writeEvent(w, "response.function_call_arguments.delta", FunctionCallArgumentsDeltaEvent{
				Type:           "response.function_call_arguments.delta",
				SequenceNumber: nextSeq(),
				ItemID:         event.ToolCallID,
				OutputIndex:    event.OutputIndex,
				Delta:          event.Delta,
			})

		case StreamEventFunctionCallArgumentsDone:
			return writeEvent(w, "response.function_call_arguments.done", FunctionCallArgumentsDoneEvent{
				Type:           "response.function_call_arguments.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ToolCallID,
				Name:           event.ToolCallName,
				OutputIndex:    event.OutputIndex,
				Arguments:      event.Arguments,
			})

		case StreamEventFunctionCallDone:
			return writeEvent(w, "response.output_item.done", FunctionCallOutputItemDoneEvent{
				Type:           "response.output_item.done",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &FunctionCallOutputItem{
					ID:        event.ToolCallID,
					Type:      "function_call",
					Status:    "completed",
					CallID:    event.ToolCallID,
					Name:      event.ToolCallName,
					Arguments: event.Arguments,
				},
			})

		case StreamEventReasoningItemAdded:
			return writeEvent(w, "response.output_item.added", ReasoningOutputItemAddedEvent{
				Type:           "response.output_item.added",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &ReasoningOutputItem{
					ID:      event.ReasoningID,
					Type:    "reasoning",
					Status:  "in_progress",
					Summary: []ReasoningOutputSummary{},
					Content: []ReasoningOutputContentPart{},
				},
			})

		case StreamEventReasoningContentPartAdded:
			return writeEvent(w, "response.content_part.added", ReasoningContentPartAddedEvent{
				Type:           "response.content_part.added",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Part: &ReasoningOutputContentPart{
					Type: "reasoning_text",
					Text: "",
				},
			})

		case StreamEventReasoningTextDelta:
			return writeEvent(w, "response.reasoning_text.delta", ReasoningTextDeltaEvent{
				Type:           "response.reasoning_text.delta",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Delta:          event.Delta,
			})

		case StreamEventReasoningTextDone:
			return writeEvent(w, "response.reasoning_text.done", ReasoningTextDoneEvent{
				Type:           "response.reasoning_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Text:           event.ReasoningText,
			})

		case StreamEventReasoningContentPartDone:
			return writeEvent(w, "response.content_part.done", ReasoningContentPartDoneEvent{
				Type:           "response.content_part.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Part: &ReasoningOutputContentPart{
					Type: "reasoning_text",
					Text: event.ReasoningText,
				},
			})

		case StreamEventReasoningSummaryPartAdded:
			return writeEvent(w, "response.reasoning_summary_part.added", ReasoningSummaryPartAddedEvent{
				Type:           "response.reasoning_summary_part.added",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Part: &ReasoningOutputSummary{
					Type: "summary_text",
					Text: "",
				},
			})

		case StreamEventReasoningSummaryDelta:
			return writeEvent(w, "response.reasoning_summary_text.delta", ReasoningSummaryTextDeltaEvent{
				Type:           "response.reasoning_summary_text.delta",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Delta:          event.Delta,
			})

		case StreamEventReasoningSummaryDone:
			return writeEvent(w, "response.reasoning_summary_text.done", ReasoningSummaryTextDoneEvent{
				Type:           "response.reasoning_summary_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Text:           event.ReasoningSummary,
			})

		case StreamEventReasoningSummaryPartDone:
			return writeEvent(w, "response.reasoning_summary_part.done", ReasoningSummaryPartDoneEvent{
				Type:           "response.reasoning_summary_part.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Part: &ReasoningOutputSummary{
					Type: "summary_text",
					Text: event.ReasoningSummary,
				},
			})

		case StreamEventReasoningItemDone:
			item := &ReasoningOutputItem{
				ID:     event.ReasoningID,
				Type:   "reasoning",
				Status: "completed",

				Summary: []ReasoningOutputSummary{},
				Content: []ReasoningOutputContentPart{},

				EncryptedContent: event.ReasoningSignature,
			}
			if event.ReasoningSummary != "" {
				item.Summary = append(item.Summary, ReasoningOutputSummary{
					Type: "summary_text",
					Text: event.ReasoningSummary,
				})
			}
			if event.ReasoningText != "" {
				item.Content = append(item.Content, ReasoningOutputContentPart{
					Type: "reasoning_text",
					Text: event.ReasoningText,
				})
			}
			return writeEvent(w, "response.output_item.done", ReasoningOutputItemDoneEvent{
				Type:           "response.output_item.done",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item:           item,
			})

		case StreamEventOutputItemDone:
			return writeEvent(w, "response.output_item.done", OutputItemDoneEvent{
				Type:           "response.output_item.done",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &OutputItem{
					ID:     messageID,
					Type:   "message",
					Status: "completed",
					Content: []OutputContent{
						{
							Type: "output_text",
							Text: event.Text,
						},
					},
					Role: MessageRoleAssistant,
				},
			})

		case StreamEventResponseCompleted:
			model := req.Model
			if event.Completion != nil && event.Completion.Model != "" {
				model = event.Completion.Model
			}

			output := []ResponseOutput{}

			if event.Completion != nil && event.Completion.Message != nil {
				// Add reasoning output if present
				for _, content := range event.Completion.Message.Content {
					if content.Reasoning != nil {
						reasoningID := event.ReasoningID
						if reasoningID == "" {
							reasoningID = "rs_" + uuid.NewString()
						}

						reasoningItem := &ReasoningOutputItem{
							ID:               reasoningID,
							Type:             "reasoning",
							Status:           "completed",
							Summary:          []ReasoningOutputSummary{},
							Content:          []ReasoningOutputContentPart{},
							EncryptedContent: event.ReasoningSignature,
						}
						if content.Reasoning.Summary != "" {
							reasoningItem.Summary = append(reasoningItem.Summary, ReasoningOutputSummary{
								Type: "summary_text",
								Text: content.Reasoning.Summary,
							})
						}
						if content.Reasoning.Text != "" {
							reasoningItem.Content = append(reasoningItem.Content, ReasoningOutputContentPart{
								Type: "reasoning_text",
								Text: content.Reasoning.Text,
							})
						}
						output = append(output, ResponseOutput{
							Type:                ResponseOutputTypeReasoning,
							ReasoningOutputItem: reasoningItem,
						})
						break
					}
				}

				// Add function call outputs (they appear before messages)
				for _, call := range event.Completion.Message.ToolCalls() {
					output = append(output, ResponseOutput{
						Type: ResponseOutputTypeFunctionCall,
						FunctionCallOutputItem: &FunctionCallOutputItem{
							ID:        call.ID,
							Type:      "function_call",
							Status:    "completed",
							Name:      call.Name,
							CallID:    call.ID,
							Arguments: call.Arguments,
						},
					})
				}

				// Add message output only if there's text content
				if event.Text != "" {
					output = append(output, ResponseOutput{
						Type: ResponseOutputTypeMessage,
						OutputMessage: &OutputMessage{
							ID:     messageID,
							Role:   MessageRoleAssistant,
							Status: "completed",
							Contents: []OutputContent{
								{
									Type: "output_text",
									Text: event.Text,
								},
							},
						},
					})
				}
			}

			response := &Response{
				ID:        responseID,
				Object:    "response",
				CreatedAt: createdAt,
				Status:    "completed",
				Model:     model,
				Output:    output,
			}

			// Add usage statistics if requested and available
			if event.Completion != nil && event.Completion.Usage != nil {
				response.Usage = &Usage{
					InputTokens:  event.Completion.Usage.InputTokens,
					OutputTokens: event.Completion.Usage.OutputTokens,
					TotalTokens:  event.Completion.Usage.InputTokens + event.Completion.Usage.OutputTokens,
				}
			}

			return writeEvent(w, "response.completed", ResponseCompletedEvent{
				Type:           "response.completed",
				SequenceNumber: nextSeq(),
				Response:       response,
			})

		case StreamEventResponseFailed:
			return writeEvent(w, "response.failed", ResponseFailedEvent{
				Type:           "response.failed",
				SequenceNumber: nextSeq(),
				Response: &Response{
					ID:        responseID,
					Object:    "response",
					CreatedAt: createdAt,
					Status:    "failed",
					Model:     req.Model,
					Output:    []ResponseOutput{},
					Error: &ResponseError{
						Type:    "server_error",
						Message: event.Error.Error(),
					},
				},
			})
		}

		return nil
	})

	// Iterate over completions from the provider
	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			accumulator.Error(err)
			return
		}

		if err := accumulator.Add(*completion); err != nil {
			accumulator.Error(err)
			return
		}
	}

	// Emit final events
	if err := accumulator.Complete(); err != nil {
		accumulator.Error(err)
		return
	}

	// Send done marker to signal end of stream
	_, _ = w.Write([]byte("data: [DONE]\n\n"))

	if rc := http.NewResponseController(w); rc != nil {
		rc.Flush()
	}
}

func (h *Handler) handleResponsesComplete(w http.ResponseWriter, r *http.Request, req ResponsesRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	acc := provider.CompletionAccumulator{}

	for c, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		acc.Add(*c)
	}

	completion := acc.Result()

	responseID := completion.ID
	if responseID == "" {
		responseID = "resp_" + uuid.NewString()
	}

	messageID := "msg_" + uuid.NewString()

	result := Response{
		Object: "response",
		Status: "completed",

		ID: responseID,

		Model:     completion.Model,
		CreatedAt: time.Now().Unix(),

		Output: []ResponseOutput{},
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Message != nil {
		// Add reasoning output first if present
		for _, content := range completion.Message.Content {
			if content.Reasoning != nil {
				reasoningItem := &ReasoningOutputItem{
					ID:     "rs_" + uuid.NewString(),
					Type:   "reasoning",
					Status: "completed",

					Summary: []ReasoningOutputSummary{},
					Content: []ReasoningOutputContentPart{},
				}

				if content.Reasoning.Summary != "" {
					reasoningItem.Summary = append(reasoningItem.Summary, ReasoningOutputSummary{
						Type: "summary_text",
						Text: content.Reasoning.Summary,
					})
				}

				if content.Reasoning.Text != "" {
					reasoningItem.Content = append(reasoningItem.Content, ReasoningOutputContentPart{
						Type: "reasoning_text",
						Text: content.Reasoning.Text,
					})
				}

				if content.Reasoning.Signature != "" {
					reasoningItem.EncryptedContent = content.Reasoning.Signature
				}

				result.Output = append(result.Output, ResponseOutput{
					Type:                ResponseOutputTypeReasoning,
					ReasoningOutputItem: reasoningItem,
				})
				break // Only one reasoning block
			}
		}

		// Add function call outputs
		for _, call := range completion.Message.ToolCalls() {
			result.Output = append(result.Output, ResponseOutput{
				Type: ResponseOutputTypeFunctionCall,
				FunctionCallOutputItem: &FunctionCallOutputItem{
					ID:        call.ID,
					Type:      "function_call",
					Status:    "completed",
					Name:      call.Name,
					CallID:    call.ID,
					Arguments: call.Arguments,
				},
			})
		}

		// Add message output only if there's text content
		if text := completion.Message.Text(); text != "" {
			output := ResponseOutput{
				Type: ResponseOutputTypeMessage,
				OutputMessage: &OutputMessage{
					ID:   messageID,
					Role: "assistant",

					Status: "completed",

					Contents: []OutputContent{
						{
							Type: "output_text",
							Text: text,
						},
					},
				},
			}

			result.Output = append(result.Output, output)
		}
	}

	if completion.Usage != nil {
		result.Usage = &Usage{
			InputTokens:  completion.Usage.InputTokens,
			OutputTokens: completion.Usage.OutputTokens,
			TotalTokens:  completion.Usage.InputTokens + completion.Usage.OutputTokens,
		}
	}

	writeJson(w, result)
}
