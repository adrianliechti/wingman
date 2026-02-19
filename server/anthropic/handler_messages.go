package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

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

	tools := toTools(req.Tools)

	options := &provider.CompleteOptions{
		Tools: tools,

		Stop:        req.StopSequences,
		Temperature: req.Temperature,
	}

	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto":
			options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceAuto}

		case "any":
			options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceAny}

		case "tool":
			options.ToolOptions = &provider.ToolOptions{
				Choice:  provider.ToolChoiceAny,
				Allowed: []string{req.ToolChoice.Name},
			}
		}
	}

	if req.MaxTokens > 0 {
		options.MaxTokens = &req.MaxTokens
	}

	// Handle structured output via output_format
	// Support both explicit type: "json_schema" and SDK format (just schema field)
	if req.OutputFormat != nil && (req.OutputFormat.Type == "json_schema" || req.OutputFormat.Schema != nil) {
		name := req.OutputFormat.Name
		if name == "" {
			name = "response" // default name for providers that require it
		}

		options.Schema = &provider.Schema{
			Name:   name,
			Schema: req.OutputFormat.Schema,
			Strict: req.OutputFormat.Strict,
		}
	}

	if req.Stream {
		h.handleMessagesStream(w, r, req, completer, messages, options)
	} else {
		h.handleMessagesComplete(w, r, req, completer, messages, options)
	}
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

		StopReason: StopReasonEndTurn,
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Usage != nil {
		result.Usage = Usage{
			InputTokens:  completion.Usage.InputTokens,
			OutputTokens: completion.Usage.OutputTokens,
		}
	}

	if completion.Message != nil {
		result.Content = toContentBlocks(completion.Message.Content)
		result.StopReason = toStopReason(completion.Message.Content)
	}

	writeJson(w, result)
}

func (h *Handler) handleMessagesStream(w http.ResponseWriter, r *http.Request, req MessageRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	messageID := generateMessageID()
	model := req.Model

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
}
