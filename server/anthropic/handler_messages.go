package anthropic

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func ptr[T any](v T) *T { return &v }

func thinkingEnabled(options *provider.CompleteOptions) bool {
	reasoning := options.ReasoningOptions

	if reasoning == nil || reasoning.Type == provider.ReasoningTypeDisabled {
		return false
	}

	return reasoning.Type == provider.ReasoningTypeAdaptive || reasoning.Effort != ""
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	var req MessageRequest

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

	system, err := parseSystemContent(req.System)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(system, req.Messages)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options, err := toCompleteOptions(req)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Stream {
		h.handleMessagesStream(w, r, req, completer, messages, options)
	} else {
		h.handleMessagesComplete(w, r, req, completer, messages, options)
	}
}

func toCompleteOptions(req MessageRequest) (*provider.CompleteOptions, error) {
	tools, err := toTools(req.Tools)

	if err != nil {
		return nil, err
	}

	options := &provider.CompleteOptions{
		Tools: tools,

		Stop:        req.StopSequences,
		Temperature: req.Temperature,
	}

	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "none":
			options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceNone}

		case "auto":
			options.ToolOptions = &provider.ToolOptions{
				Choice:                   provider.ToolChoiceAuto,
				DisableParallelToolCalls: req.ToolChoice.DisableParallelToolUse,
			}

		case "any":
			options.ToolOptions = &provider.ToolOptions{
				Choice:                   provider.ToolChoiceAny,
				DisableParallelToolCalls: req.ToolChoice.DisableParallelToolUse,
			}

		case "tool":
			options.ToolOptions = &provider.ToolOptions{
				Choice:                   provider.ToolChoiceAny,
				Allowed:                  []string{req.ToolChoice.Name},
				DisableParallelToolCalls: req.ToolChoice.DisableParallelToolUse,
			}
		}
	}

	if req.MaxTokens > 0 {
		options.MaxTokens = &req.MaxTokens
	}

	// Handle structured output via output_config.format (canonical) or the
	// deprecated top-level output_format.
	// Support both explicit type: "json_schema" and SDK format (just schema field)
	format := req.OutputFormat

	if req.OutputConfig != nil && req.OutputConfig.Format != nil {
		format = req.OutputConfig.Format
	}

	if format != nil && (format.Type == "json_schema" || format.Schema != nil) {
		name := format.Name
		if name == "" {
			name = "response" // default name for providers that require it
		}

		options.Schema = &provider.Schema{
			Name:       name,
			Strict:     format.Strict,
			Properties: format.Schema,
		}
	}

	var reasoningType provider.ReasoningType
	var reasoningEffort provider.Effort

	if req.OutputConfig != nil {
		switch req.OutputConfig.Effort {
		case "low":
			reasoningEffort = provider.EffortLow
		case "medium":
			reasoningEffort = provider.EffortMedium
		case "high":
			reasoningEffort = provider.EffortHigh
		case "xhigh":
			reasoningEffort = provider.EffortXHigh
		case "max":
			reasoningEffort = provider.EffortMax
		}
	}

	if req.Thinking != nil {
		switch req.Thinking.Type {
		case "disabled":
			reasoningType = provider.ReasoningTypeDisabled
		case "enabled":
			reasoningType = provider.ReasoningTypeAdaptive

			if reasoningEffort == "" && req.Thinking.BudgetTokens > 0 {
				// Legacy fixed-budget thinking carries no effort level; derive one
				// from the token budget so it round-trips to non-Anthropic backends.
				reasoningEffort = provider.EffortFromBudget(&req.Thinking.BudgetTokens)
			}
		default:
			reasoningType = provider.ReasoningTypeAdaptive
		}
	}

	if reasoningType != "" || reasoningEffort != "" {
		summary := req.Thinking == nil || req.Thinking.Display != "omitted"

		options.ReasoningOptions = &provider.ReasoningOptions{
			Type:   reasoningType,
			Effort: reasoningEffort,

			IncludeSummary:   summary,
			IncludeSignature: true,
		}
	}

	if req.ContextManagement != nil {
		for _, edit := range req.ContextManagement.Edits {
			if strings.HasPrefix(edit.Type, "compact") {
				options.CompactionOptions = &provider.CompactionOptions{}

				// Without an explicit trigger the upstream default applies.
				if edit.Trigger != nil {
					options.CompactionOptions.Threshold = edit.Trigger.Value
				}

				break
			}
		}
	}

	return options, nil
}

func (h *Handler) handleMessagesComplete(w http.ResponseWriter, r *http.Request, req MessageRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		acc.Add(*completion)
	}

	completion := acc.Result()

	result := Message{
		ID: generateMessageID(),

		Type: "message",
		Role: "assistant",

		Model:   completion.Model,
		Content: []ContentBlock{},

		StopReason: ptr(StopReasonEndTurn),
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Usage != nil {
		result.Usage = Usage{
			InputTokens:  completion.Usage.InputTokens,
			OutputTokens: completion.Usage.OutputTokens,

			CacheReadInputTokens:     completion.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: completion.Usage.CacheCreationInputTokens,
		}

		if completion.Usage.ReasoningTokens > 0 || thinkingEnabled(options) {
			result.Usage.OutputTokensDetails = &OutputTokensDetails{
				ThinkingTokens: completion.Usage.ReasoningTokens,
			}
		}
	}

	if completion.Message != nil {
		result.Content = toContentBlocks(completion.Message.Content, thinkingEnabled(options))
		reason := toStopReason(completion)
		result.StopReason = &reason

		if reason == StopReasonStopSequence {
			result.StopSequence = &completion.StopSequence
		}

		if reason == StopReasonRefusal {
			result.StopDetails = &StopDetails{Type: "refusal"}

			if completion.StopDetails != nil {
				result.StopDetails.Category = completion.StopDetails.Category
				result.StopDetails.Explanation = completion.StopDetails.Explanation
			}
		}
	}

	writeJson(w, result)
}

func (h *Handler) handleMessagesStream(w http.ResponseWriter, r *http.Request, req MessageRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	messageID := generateMessageID()
	model := req.Model

	headersSent := false

	sendHeaders := func() {
		if !headersSent {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			headersSent = true
		}
	}

	// Create streaming accumulator with event handler
	accumulator := NewStreamingAccumulator(messageID, model, func(event StreamEvent) error {
		switch event.Type {
		case StreamEventMessageStart:
			return writeEvent(w, "message_start", MessageStartEvent{
				Type:    "message_start",
				Message: *event.Message,
			})

		case StreamEventContentBlockStart:
			return writeEvent(w, "content_block_start", ContentBlockStartEvent{
				Type:         "content_block_start",
				Index:        event.Index,
				ContentBlock: *event.ContentBlock,
			})

		case StreamEventContentBlockDelta:
			return writeEvent(w, "content_block_delta", ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: event.Index,
				Delta: *event.Delta,
			})

		case StreamEventContentBlockStop:
			return writeEvent(w, "content_block_stop", ContentBlockStopEvent{
				Type:  "content_block_stop",
				Index: event.Index,
			})

		case StreamEventMessageDelta:
			return writeEvent(w, "message_delta", MessageDeltaEvent{
				Type:  "message_delta",
				Delta: *event.MessageDelta,
				Usage: *event.DeltaUsage,
			})

		case StreamEventMessageStop:
			return writeEvent(w, "message_stop", MessageStopEvent{
				Type: "message_stop",
			})

		case StreamEventError:
			return writeEvent(w, "error", ErrorResponse{
				Type:  "error",
				Error: *event.Error,
			})
		}

		return nil
	})

	accumulator.ThinkingEnabled = thinkingEnabled(options)

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			if !headersSent {
				writeError(w, http.StatusBadRequest, err)
				return
			}

			writeSSERetry(w, err)
			accumulator.Error(err)
			return
		}

		sendHeaders()

		if err := accumulator.Add(*completion); err != nil {
			accumulator.Error(err)
			return
		}
	}

	sendHeaders()

	// Emit final events
	if err := accumulator.Complete(); err != nil {
		writeSSERetry(w, err)
		accumulator.Error(err)
		return
	}
}
