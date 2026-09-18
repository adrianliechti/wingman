package anthropic

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	var req MessageRequest
	if err := decodeRequest(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("model and non-empty messages are required"))
		return
	}

	if err := validateMessageRequest(req); err != nil {
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
	if err := validateCompactionRequest(req); err != nil {
		return nil, err
	}
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

	if req.MaxTokens != nil {
		options.MaxTokens = req.MaxTokens
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
	} else {
		// Request replayable state without overriding the model's thinking
		// defaults. Providers that always return signatures need no extra flag.
		options.ReasoningOptions = &provider.ReasoningOptions{IncludeSignature: true}
	}

	if req.ContextManagement != nil {
		for _, edit := range req.ContextManagement.Edits {
			if edit.Type == "clear_thinking_20251015" {
				options.ReasoningOptions.Context, _ = edit.reasoningContext() // Validated above.
			}
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
	if req.Compaction != nil {
		options.CompactionOptions = &provider.CompactionOptions{
			Trigger: true,
		}
	}

	return options, nil
}

func validateCompactionRequest(req MessageRequest) error {
	if req.Compaction != nil {
		if req.Compaction.Instructions != "" {
			return fmt.Errorf("compaction.instructions: custom compaction instructions are not supported")
		}
		if req.Compaction.Type != "summarize" {
			return fmt.Errorf("compaction.type: must be summarize")
		}
		if req.ContextManagement != nil {
			return fmt.Errorf("compaction cannot be combined with context_management")
		}
		if len(req.StopSequences) > 0 || req.OutputFormat != nil || (req.OutputConfig != nil && req.OutputConfig.Format != nil) ||
			(req.ToolChoice != nil && (req.ToolChoice.Type == "any" || req.ToolChoice.Type == "tool")) {
			return fmt.Errorf("compaction cannot be combined with stop_sequences, output format, or forced tool_choice")
		}
	}
	if req.ContextManagement != nil {
		seen := map[string]bool{}
		for i, edit := range req.ContextManagement.Edits {
			if seen[edit.Type] {
				return fmt.Errorf("context_management.edits: duplicate edit %q", edit.Type)
			}
			seen[edit.Type] = true
			if edit.Type == "clear_thinking_20251015" {
				if i != 0 || edit.Trigger != nil || edit.Instructions != "" || edit.PauseAfterCompaction {
					return fmt.Errorf("context_management: thinking retention must be first and may only configure keep")
				}
				if _, err := edit.reasoningContext(); err != nil {
					return err
				}
				continue
			}
			if edit.Type != "compact_20260112" {
				return fmt.Errorf("context_management.edits: unsupported edit %q", edit.Type)
			}
			if edit.Instructions != "" || edit.PauseAfterCompaction {
				return fmt.Errorf("context_management: custom compaction instructions and pause_after_compaction are not supported; use compaction.type=summarize for explicit compaction")
			}
			if len(edit.Keep) > 0 {
				return fmt.Errorf("context_management: compaction does not support keep")
			}
			if edit.Type == "compact_20260112" && edit.Trigger != nil && (edit.Trigger.Type != "input_tokens" || edit.Trigger.Value < 50000) {
				return fmt.Errorf("context_management: compaction trigger must be input_tokens with value at least 50000")
			}
		}
	}
	return nil
}

func validateMessageRequest(req MessageRequest) error {
	if req.OutputConfig != nil && len(req.OutputConfig.TaskBudget) > 0 && string(req.OutputConfig.TaskBudget) != "null" {
		return fmt.Errorf("output_config.task_budget: task-wide budgets are not supported")
	}
	if req.OutputConfig != nil && req.OutputConfig.Effort != "" && !validEffort(req.OutputConfig.Effort) {
		return fmt.Errorf("output_config.effort: unsupported effort %q", req.OutputConfig.Effort)
	}
	if req.Thinking != nil {
		if len(req.Thinking.BlockBinding) > 0 && string(req.Thinking.BlockBinding) != "null" {
			return fmt.Errorf("thinking.block_binding: binding controls are not supported")
		}
		switch req.Thinking.Type {
		case "enabled", "adaptive", "disabled":
		default:
			return fmt.Errorf("thinking.type: must be enabled, adaptive, or disabled")
		}
		switch req.Thinking.Display {
		case "", "summarized", "omitted":
		default:
			return fmt.Errorf("thinking.display: only summarized and omitted are supported")
		}
	}
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "any", "none":
		case "tool":
			if req.ToolChoice.Name == "" {
				return fmt.Errorf("tool_choice.name: required for type tool")
			}
		default:
			return fmt.Errorf("tool_choice.type: must be auto, any, tool, or none")
		}
	}
	if req.MaxTokens == nil {
		return fmt.Errorf("max_tokens: Field required")
	}

	if *req.MaxTokens < 0 {
		return fmt.Errorf("max_tokens: Must be greater than or equal to 0")
	}

	if *req.MaxTokens != 0 {
		return nil
	}

	if req.Stream {
		return fmt.Errorf("stream: Cannot be enabled when max_tokens is 0")
	}

	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		return fmt.Errorf("thinking: Cannot be enabled when max_tokens is 0")
	}

	if req.OutputFormat != nil || (req.OutputConfig != nil && req.OutputConfig.Format != nil) {
		return fmt.Errorf("output_config.format: Cannot be set when max_tokens is 0")
	}

	if req.ToolChoice != nil && (req.ToolChoice.Type == "any" || req.ToolChoice.Type == "tool") {
		return fmt.Errorf("tool_choice: Cannot force tool use when max_tokens is 0")
	}

	return nil
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

		StopReason: new(StopReasonEndTurn),
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Usage != nil {
		result.Usage = Usage{
			// The intermediate Usage.InputTokens is cache-inclusive; Anthropic's
			// wire input_tokens excludes cached tokens, so subtract them back out.
			InputTokens:  anthropicInputTokens(completion.Usage),
			OutputTokens: completion.Usage.OutputTokens,

			CacheReadInputTokens:     completion.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: completion.Usage.CacheCreationInputTokens,
		}

		if completion.Usage.ReasoningTokens != nil {
			result.Usage.OutputTokensDetails = &OutputTokensDetails{
				ThinkingTokens: *completion.Usage.ReasoningTokens,
			}
		}
	}

	if completion.Message != nil {
		result.Content = toContentBlocks(completion.Message.Content)
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

// anthropicInputTokens converts the cache-inclusive intermediate input-token
// count into Anthropic's wire convention, where input_tokens excludes tokens
// served from or written to the cache (those are reported separately). Clamped
// at zero to stay robust against any upstream rounding.
func anthropicInputTokens(usage *provider.Usage) int {
	tokens := usage.InputTokens - usage.CacheReadInputTokens - usage.CacheCreationInputTokens
	if tokens < 0 {
		return 0
	}
	return tokens
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
