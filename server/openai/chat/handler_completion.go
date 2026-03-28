package chat

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/policy"
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

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, req.Model, policy.ActionAccess); err != nil {
		writeError(w, http.StatusNotFound, err)
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

		ToolOptions: toToolOptions(req.ToolChoice),

		MaxTokens:   req.MaxCompletionTokens,
		Temperature: req.Temperature,
	}

	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		if options.ToolOptions == nil {
			options.ToolOptions = &provider.ToolOptions{Choice: provider.ToolChoiceAuto}
		}

		options.ToolOptions.DisableParallelToolCalls = true
	}

	if req.ReasoningEffort != "" {
		if options.ReasoningOptions == nil {
			options.ReasoningOptions = &provider.ReasoningOptions{}
		}

		switch req.ReasoningEffort {
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

	if req.Verbosity != "" {
		if options.OutputOptions == nil {
			options.OutputOptions = &provider.OutputOptions{}
		}

		switch req.Verbosity {
		case VerbosityLow:
			options.OutputOptions.Verbosity = provider.VerbosityLow

		case VerbosityMedium:
			options.OutputOptions.Verbosity = provider.VerbosityMedium

		case VerbosityHigh:
			options.OutputOptions.Verbosity = provider.VerbosityHigh
		}
	}

	if req.ResponseFormat != nil {
		if req.ResponseFormat.Type == ResponseFormatJSONObject {
			options.Schema = &provider.Schema{
				Name: "json_object",
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

		ServiceTier: "default",
	}

	if result.Model == "" {
		result.Model = req.Model
	}

	if completion.Message != nil {
		message := &ChatCompletionMessage{
			Role:        role,
			Annotations: []any{},
		}

		if content := completion.Message.Text(); content != "" {
			message.Content = &content
		}

		if refusal := completion.Message.Refusal(); refusal != "" {
			message.Refusal = &refusal
		}

		reason := FinishReasonStop

		if completion.Status == provider.CompletionStatusRefused {
			reason = FinishReasonStop
		}

		if calls := oaiToolCalls(completion.Message.Content); len(calls) > 0 {
			reason = FinishReasonToolCalls
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

	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage != nil && *req.StreamOptions.IncludeUsage

	// Create streaming accumulator with event handler
	accumulator := NewStreamingAccumulator(req.Model, func(event StreamEvent) error {
		switch event.Type {
		case StreamEventUsage:
			if !includeUsage {
				return nil
			}

			event.Chunk.Created = time.Now().Unix()
			return writeEvent(w, event.Chunk)

		case StreamEventChunk, StreamEventFinish:
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
