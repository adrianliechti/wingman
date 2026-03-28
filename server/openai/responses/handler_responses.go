package responses

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/policy"
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

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	messages, err := toMessages(req.Input.Items, req.Instructions)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools := toTools(req.Tools)

	options := &provider.CompleteOptions{
		Tools:       tools,
		ToolOptions: toToolOptions(req.ToolChoice),

		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
	}

	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		if options.ToolOptions == nil {
			options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceAuto}
		}

		options.ToolOptions.DisableParallelToolCalls = true
	}

	for _, inc := range req.Include {
		if inc == "reasoning.encrypted_content" {
			if options.ReasoningOptions == nil {
				options.ReasoningOptions = &provider.ReasoningOptions{}
			}

			options.ReasoningOptions.IncludeSignature = true
			break
		}
	}

	if req.Reasoning != nil && req.Reasoning.Summary != nil {
		if summary, ok := (*req.Reasoning.Summary).(string); ok && summary != "" && summary != "disabled" {
			if options.ReasoningOptions == nil {
				options.ReasoningOptions = &provider.ReasoningOptions{}
			}

			options.ReasoningOptions.IncludeSummary = true
		}
	}

	if req.Reasoning != nil && req.Reasoning.Effort != nil {
		if options.ReasoningOptions == nil {
			options.ReasoningOptions = &provider.ReasoningOptions{}
		}

		switch *req.Reasoning.Effort {
		case ReasoningEffortNone:
			options.ReasoningOptions.Effort = provider.EffortNone

		case ReasoningEffortMinimal:
			options.ReasoningOptions.Effort = provider.EffortMinimal

		case ReasoningEffortLow:
			options.ReasoningOptions.Effort = provider.EffortLow

		case ReasoningEffortMedium:
			options.ReasoningOptions.Effort = provider.EffortMedium

		case ReasoningEffortHigh:
			options.ReasoningOptions.Effort = provider.EffortHigh

		case ReasoningEffortXHigh:
			options.ReasoningOptions.Effort = provider.EffortMax
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

		if req.Text.Verbosity != "" {
			if options.OutputOptions == nil {
				options.OutputOptions = &provider.OutputOptions{}
			}

			switch req.Text.Verbosity {
			case VerbosityLow:
				options.OutputOptions.Verbosity = provider.VerbosityLow

			case VerbosityMedium:
				options.OutputOptions.Verbosity = provider.VerbosityMedium

			case VerbosityHigh:
				options.OutputOptions.Verbosity = provider.VerbosityHigh
			}
		}
	}

	for _, cm := range req.ContextManagement {
		if cm.Type == "compaction" && cm.CompactThreshold != nil {
			options.CompactionOptions = &provider.CompactionOptions{
				Threshold: int(*cm.CompactThreshold),
			}
			break
		}
	}

	if req.Stream {
		h.handleResponsesStream(w, r, req, completer, messages, options)
	} else {
		h.handleResponsesComplete(w, r, req, completer, messages, options)
	}
}

func responseStatus(status provider.CompletionStatus) string {
	switch status {
	case provider.CompletionStatusIncomplete:
		return "incomplete"
	case provider.CompletionStatusFailed:
		return "failed"
	default:
		return "completed"
	}
}

func responseModel(completion *provider.Completion, defaultModel string) string {
	if completion != nil && completion.Model != "" {
		return completion.Model
	}

	return defaultModel
}

func responseUsage(usage *provider.Usage) *Usage {
	if usage == nil {
		return nil
	}

	return &Usage{
		InputTokens:        usage.InputTokens,
		InputTokensDetails: &InputTokensDetails{},

		OutputTokens:        usage.OutputTokens,
		OutputTokensDetails: &OutputTokensDetails{},

		TotalTokens: usage.InputTokens + usage.OutputTokens,
	}
}

// responseDefaults populates the OpenAI-compatible default fields on a Response.
func responseDefaults(resp *Response, req ResponsesRequest) {
	resp.Object = "response"
	resp.Background = false
	resp.Store = false
	resp.ServiceTier = "default"

	resp.ParallelToolCalls = true
	if req.ParallelToolCalls != nil {
		resp.ParallelToolCalls = *req.ParallelToolCalls
	}

	resp.Temperature = 1.0
	if req.Temperature != nil {
		resp.Temperature = *req.Temperature
	}

	resp.TopP = 0.98
	resp.FrequencyPenalty = 0.0
	resp.PresencePenalty = 0.0
	resp.TopLogprobs = 0
	resp.Truncation = "disabled"

	if req.Instructions != "" {
		resp.Instructions = &req.Instructions
	}

	resp.MaxOutputTokens = req.MaxOutputTokens

	if req.Reasoning != nil {
		resp.Reasoning = req.Reasoning
	} else {
		effort := ReasoningEffortNone
		resp.Reasoning = &ReasoningConfig{
			Effort: &effort,
		}
	}

	if resp.Reasoning.Effort == nil {
		effort := ReasoningEffortNone
		resp.Reasoning.Effort = &effort
	}

	if req.Text != nil {
		resp.Text = req.Text
	} else {
		resp.Text = &TextConfig{
			Format: &TextFormat{Type: "text"},
		}
	}

	if resp.Text.Verbosity == "" {
		resp.Text.Verbosity = VerbosityMedium
	}


	if req.ToolChoice != nil {
		// Echo tool_choice as a string when it's a simple mode (matching OpenAI behavior)
		if len(req.ToolChoice.AllowedTools) == 0 && req.ToolChoice.Mode != "" {
			resp.ToolChoice = string(req.ToolChoice.Mode)
		} else {
			resp.ToolChoice = req.ToolChoice
		}
	} else {
		resp.ToolChoice = "auto"
	}

	if len(req.Tools) > 0 {
		tools := make([]any, len(req.Tools))
		for i, t := range req.Tools {
			// OpenAI echoes tools with strict=true and additionalProperties=false
			if t.Strict == nil {
				strict := true
				t.Strict = &strict
			}

			if t.Parameters != nil {
				if _, ok := t.Parameters["additionalProperties"]; !ok {
					t.Parameters["additionalProperties"] = false
				}
			}

			tools[i] = t
		}
		resp.Tools = tools
	} else {
		resp.Tools = []any{}
	}

	if resp.Metadata == nil {
		resp.Metadata = map[string]any{}
	}

	if resp.Output == nil {
		resp.Output = []ResponseOutput{}
	}
}

// reasoningRequested returns true if the request explicitly asks for reasoning output.
func reasoningRequested(req ResponsesRequest) bool {
	if req.Reasoning == nil {
		return false
	}

	if req.Reasoning.Effort != nil && *req.Reasoning.Effort != ReasoningEffortNone {
		return true
	}

	if req.Reasoning.Summary != nil {
		return true
	}

	return false
}

type responseOutputOptions struct {
	IncludeSummary   bool
	IncludeReasoning bool
}

func responseOutputs(message *provider.Message, messageID, status string, opts responseOutputOptions) []ResponseOutput {
	if message == nil {
		return []ResponseOutput{}
	}

	output := []ResponseOutput{}

	for _, content := range message.Content {
		if content.Reasoning != nil && !opts.IncludeReasoning {
			continue
		}

		if content.Reasoning != nil && (content.Reasoning.ID != "" || content.Reasoning.Text != "" || content.Reasoning.Summary != "" || content.Reasoning.Signature != "") {
			reasoningID := content.Reasoning.ID
			if reasoningID == "" {
				reasoningID = "rs_" + uuid.NewString()
			}

			reasoningItem := &ReasoningOutputItem{
				ID: reasoningID,

				Type:   "reasoning",
				Status: status,

				Summary: []ReasoningOutputSummary{},
				Content: []ReasoningOutputContentPart{},

				EncryptedContent: content.Reasoning.Signature,
			}

			reasoningText := content.Reasoning.Text
			reasoningSummary := content.Reasoning.Summary

			if opts.IncludeSummary && reasoningText != "" && reasoningSummary == "" {
				reasoningSummary = reasoningText
				reasoningText = ""
			}

			if reasoningSummary != "" {
				reasoningItem.Summary = append(reasoningItem.Summary, ReasoningOutputSummary{
					Type: "summary_text",
					Text: reasoningSummary,
				})
			}

			if reasoningText != "" {
				reasoningItem.Content = append(reasoningItem.Content, ReasoningOutputContentPart{
					Type: "reasoning_text",
					Text: reasoningText,
				})
			}

			output = append(output, ResponseOutput{
				Type:                ResponseOutputTypeReasoning,
				ReasoningOutputItem: reasoningItem,
			})
			break
		}
	}

	if text := message.Text(); text != "" {
		output = append(output, ResponseOutput{
			Type: ResponseOutputTypeMessage,
			OutputMessage: &OutputMessage{
				ID:     messageID,
				Role:   MessageRoleAssistant,
				Status: status,
				Phase:  "final_answer",
				Contents: []OutputContent{
					{
						Type:        "output_text",
						Text:        text,
						Annotations: []any{},
						Logprobs:    []any{},
					},
				},
			},
		})
	}

	for _, call := range message.ToolCalls() {
		output = append(output, ResponseOutput{
			Type: ResponseOutputTypeFunctionCall,
			FunctionCallOutputItem: &FunctionCallOutputItem{
				ID:        "fc_" + call.ID,
				Type:      "function_call",
				Status:    status,
				Name:      call.Name,
				CallID:    call.ID,
				Arguments: call.Arguments,
			},
		})
	}

	for _, content := range message.Content {
		if content.Compaction != nil && content.Compaction.Signature != "" {
			output = append(output, ResponseOutput{
				Type: ResponseOutputTypeCompaction,
				CompactionOutputItem: &CompactionOutputItem{
					ID: content.Compaction.ID,

					Type:             "compaction",
					EncryptedContent: content.Compaction.Signature,
				},
			})
		}
	}

	return output
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

	outputOpts := responseOutputOptions{
		IncludeSummary:   options.ReasoningOptions != nil && options.ReasoningOptions.IncludeSummary,
		IncludeReasoning: reasoningRequested(req),
	}

	// Create initial response template
	createResponse := func(status string, output []ResponseOutput) *Response {
		resp := &Response{
			ID:        responseID,
			CreatedAt: createdAt,
			Status:    status,
			Model:     req.Model,
			Output:    output,
		}
		responseDefaults(resp, req)
		return resp
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
					Phase:   "final_answer",
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
					Type:        "output_text",
					Text:        "",
					Annotations: []any{},
					Logprobs:    []any{},
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
				Logprobs:       []any{},
			})

		case StreamEventTextDone:
			return writeEvent(w, "response.output_text.done", OutputTextDoneEvent{
				Type:           "response.output_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Text:           event.Text,
				Logprobs:       []any{},
			})

		case StreamEventContentPartDone:
			return writeEvent(w, "response.content_part.done", ContentPartDoneEvent{
				Type:           "response.content_part.done",
				SequenceNumber: nextSeq(),
				ItemID:         messageID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   0,
				Part: &OutputContent{
					Type:        "output_text",
					Text:        event.Text,
					Annotations: []any{},
					Logprobs:    []any{},
				},
			})

		case StreamEventFunctionCallAdded:
			return writeEvent(w, "response.output_item.added", FunctionCallOutputItemAddedEvent{
				Type:           "response.output_item.added",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &FunctionCallOutputItem{
					ID:        "fc_" + event.ToolCallID,
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
				ItemID:         "fc_" + event.ToolCallID,
				OutputIndex:    event.OutputIndex,
				Delta:          event.Delta,
			})

		case StreamEventFunctionCallArgumentsDone:
			return writeEvent(w, "response.function_call_arguments.done", FunctionCallArgumentsDoneEvent{
				Type:           "response.function_call_arguments.done",
				SequenceNumber: nextSeq(),
				ItemID:         "fc_" + event.ToolCallID,
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
					ID:        "fc_" + event.ToolCallID,
					Type:      "function_call",
					Status:    "completed",
					CallID:    event.ToolCallID,
					Name:      event.ToolCallName,
					Arguments: event.Arguments,
				},
			})

		case StreamEventCompactionItemAdded:
			return writeEvent(w, "response.output_item.added", CompactionOutputItemAddedEvent{
				Type:           "response.output_item.added",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &CompactionOutputItem{
					ID:               event.CompactionID,
					Type:             "compaction",
					EncryptedContent: event.CompactionContent,
				},
			})

		case StreamEventCompactionItemDone:
			return writeEvent(w, "response.output_item.done", CompactionOutputItemDoneEvent{
				Type:           "response.output_item.done",
				SequenceNumber: nextSeq(),
				OutputIndex:    event.OutputIndex,
				Item: &CompactionOutputItem{
					ID:               event.CompactionID,
					Type:             "compaction",
					EncryptedContent: event.CompactionContent,
				},
			})

		case StreamEventReasoningItemAdded:
			if event.ReasoningID == "" {
				return nil
			}

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
			if event.ReasoningID == "" {
				return nil
			}

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
			if event.ReasoningID == "" {
				return nil
			}

			return writeEvent(w, "response.reasoning_text.delta", ReasoningTextDeltaEvent{
				Type:           "response.reasoning_text.delta",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Delta:          event.Delta,
			})

		case StreamEventReasoningTextDone:
			if event.ReasoningID == "" {
				return nil
			}

			return writeEvent(w, "response.reasoning_text.done", ReasoningTextDoneEvent{
				Type:           "response.reasoning_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				ContentIndex:   event.ContentIndex,
				Text:           event.ReasoningText,
			})

		case StreamEventReasoningContentPartDone:
			if event.ReasoningID == "" {
				return nil
			}

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
			if event.ReasoningID == "" {
				return nil
			}

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
			if event.ReasoningID == "" {
				return nil
			}

			return writeEvent(w, "response.reasoning_summary_text.delta", ReasoningSummaryTextDeltaEvent{
				Type:           "response.reasoning_summary_text.delta",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Delta:          event.Delta,
			})

		case StreamEventReasoningSummaryDone:
			if event.ReasoningID == "" {
				return nil
			}

			return writeEvent(w, "response.reasoning_summary_text.done", ReasoningSummaryTextDoneEvent{
				Type:           "response.reasoning_summary_text.done",
				SequenceNumber: nextSeq(),
				ItemID:         event.ReasoningID,
				OutputIndex:    event.OutputIndex,
				SummaryIndex:   event.SummaryIndex,
				Text:           event.ReasoningSummary,
			})

		case StreamEventReasoningSummaryPartDone:
			if event.ReasoningID == "" {
				return nil
			}

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
			if event.ReasoningID == "" {
				return nil
			}

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
							Type:        "output_text",
							Text:        event.Text,
							Annotations: []any{},
							Logprobs:    []any{},
						},
					},
					Phase: "final_answer",
					Role:  MessageRoleAssistant,
				},
			})

		case StreamEventResponseCompleted:
			now := time.Now().Unix()
			response := &Response{
				ID:          responseID,
				CreatedAt:   createdAt,
				CompletedAt: &now,
				Status:      "completed",
				Model:       responseModel(event.Completion, req.Model),
				Output:      responseOutputs(event.Completion.Message, messageID, "completed", outputOpts),
				Usage:       responseUsage(event.Completion.Usage),
			}
			responseDefaults(response, req)

			return writeEvent(w, "response.completed", ResponseCompletedEvent{
				Type:           "response.completed",
				SequenceNumber: nextSeq(),
				Response:       response,
			})

		case StreamEventResponseIncomplete:
			response := &Response{
				ID:        responseID,
				CreatedAt: createdAt,
				Status:    "incomplete",
				Model:     responseModel(event.Completion, req.Model),
				Output:    responseOutputs(event.Completion.Message, messageID, "incomplete", outputOpts),
				Usage:     responseUsage(event.Completion.Usage),
			}
			responseDefaults(response, req)

			return writeEvent(w, "response.incomplete", ResponseIncompleteEvent{
				Type:           "response.incomplete",
				SequenceNumber: nextSeq(),
				Response:       response,
			})

		case StreamEventResponseFailed:
			failResp := &Response{
				ID:        responseID,
				CreatedAt: createdAt,
				Status:    "failed",
				Model:     req.Model,
				Output:    []ResponseOutput{},
				Error: &ResponseError{
					Type:    "server_error",
					Message: event.Error.Error(),
				},
			}
			responseDefaults(failResp, req)

			return writeEvent(w, "response.failed", ResponseFailedEvent{
				Type:           "response.failed",
				SequenceNumber: nextSeq(),
				Response:       failResp,
			})
		}

		return nil
	})

	accumulator.ReasoningAsSummary = outputOpts.IncludeSummary
	accumulator.SuppressReasoning = !outputOpts.IncludeReasoning

	failed := false

	// Iterate over completions from the provider
	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			accumulator.Error(err)
			failed = true
			break
		}

		if err := accumulator.Add(*completion); err != nil {
			accumulator.Error(err)
			failed = true
			break
		}
	}

	// Emit final events
	if !failed {
		if err := accumulator.Complete(); err != nil {
			accumulator.Error(err)
		}
	}

	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	http.NewResponseController(w).Flush()
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

	now := time.Now().Unix()

	result := Response{
		ID:        responseID,
		CreatedAt: now,
		Status:    responseStatus(completion.Status),
		Model:     responseModel(completion, req.Model),
		Output: responseOutputs(completion.Message, "msg_"+uuid.NewString(), responseStatus(completion.Status), responseOutputOptions{
			IncludeSummary:   options.ReasoningOptions != nil && options.ReasoningOptions.IncludeSummary,
			IncludeReasoning: reasoningRequested(req),
		}),
		Usage:     responseUsage(completion.Usage),
	}

	if result.Status == "completed" {
		result.CompletedAt = &now
	}

	responseDefaults(&result, req)

	writeJson(w, result)
}
