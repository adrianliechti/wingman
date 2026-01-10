package chat

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var req ChatCompletionRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Messages)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var stops []string

	switch v := req.Stop.(type) {
	case string:
		stops = []string{v}

	case []string:
		stops = v
	}

	options := &provider.CompleteOptions{
		Stop:  stops,
		Tools: tools,

		MaxTokens:   req.MaxCompletionTokens,
		Temperature: req.Temperature,
	}

	switch req.ReasoningEffort {
	case ReasoningEffortMinimal:
		options.Effort = provider.EffortMinimal
	case ReasoningEffortLow:
		options.Effort = provider.EffortLow
	case ReasoningEffortMedium:
		options.Effort = provider.EffortMedium
	case ReasoningEffortHigh:
		options.Effort = provider.EffortHigh
	}

	switch req.Verbosity {
	case VerbosityLow:
		options.Verbosity = provider.VerbosityLow
	case VerbosityMedium:
		options.Verbosity = provider.VerbosityMedium
	case VerbosityHigh:
		options.Verbosity = provider.VerbosityHigh
	}

	if req.ResponseFormat != nil {
		if req.ResponseFormat.Type == ResponseFormatJSONObject {
			// Convert json_object to minimal json_schema
			options.Schema = &provider.Schema{
				Name:   "json_object",
				Schema: map[string]any{"type": "object"},
			}
		}

		if req.ResponseFormat.JSONSchema != nil {
			options.Schema = &provider.Schema{
				Name:        req.ResponseFormat.JSONSchema.Name,
				Description: req.ResponseFormat.JSONSchema.Description,

				Strict: req.ResponseFormat.JSONSchema.Strict,
				Schema: req.ResponseFormat.JSONSchema.Schema,
			}
		}
	}

	if req.Stream {
		h.handleChatCompletionStream(w, r, req, completer, messages, options)
	} else {
		h.handleChatCompletionComplete(w, r, req, completer, messages, options)
	}
}

func (h *Handler) handleChatCompletionComplete(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		acc.Add(*completion)
	}

	completion := acc.Result()

	role := MessageRoleAssistant

	result := ChatCompletion{
		Object: "chat.completion",

		ID: completion.ID,

		Model:   completion.Model,
		Created: time.Now().Unix(),

		Choices: []ChatCompletionChoice{},
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Message != nil {
		message := &ChatCompletionMessage{
			Role: role,
		}

		if content := completion.Message.Text(); content != "" {
			message.Content = &content
		}

		reason := FinishReasonStop

		if calls := oaiToolCalls(completion.Message.Content); len(calls) > 0 {
			reason = FinishReasonToolCalls

			message.Content = nil
			message.ToolCalls = calls
		}

		result.Choices = []ChatCompletionChoice{
			{
				Message:      message,
				FinishReason: &reason,
			},
		}
	}

	if completion.Usage != nil {
		result.Usage = &Usage{
			PromptTokens:     completion.Usage.InputTokens,
			CompletionTokens: completion.Usage.OutputTokens,
			TotalTokens:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
		}
	}

	writeJson(w, result)
}

func (h *Handler) handleChatCompletionStream(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest, completer provider.Completer, messages []provider.Message, options *provider.CompleteOptions) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create streaming accumulator with event handler
	accumulator := NewStreamingAccumulator(req.Model, func(event StreamEvent) error {
		switch event.Type {
		case StreamEventChunk, StreamEventFinish, StreamEventUsage:
			event.Chunk.Created = time.Now().Unix()
			return writeEvent(w, event.Chunk)

		case StreamEventDone:
			_, _ = w.Write([]byte("data: [DONE]\n\n"))

			if rc := http.NewResponseController(w); rc != nil {
				rc.Flush()
			}

			return nil

		case StreamEventError:
			return writeErrorEvent(w, event.Error)
		}

		return nil
	})

	for c, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			accumulator.Error(err)
			return
		}

		if err := accumulator.Add(*c); err != nil {
			accumulator.Error(err)
			return
		}
	}

	if err := accumulator.Complete(streamUsage(req)); err != nil {
		accumulator.Error(err)
		return
	}
}
