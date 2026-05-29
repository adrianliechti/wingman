package gemini

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")

	completer, messages, options, err := h.parseGenerateRequest(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		acc.Add(*completion)
	}

	completion := acc.Result()

	result := GenerateContentResponse{
		ResponseId:   generateResponseID(),
		ModelVersion: completion.Model,
	}

	if result.ModelVersion == "" {
		result.ModelVersion = model
	}

	result.UsageMetadata = toUsageMetadata(completion.Usage)

	if completion.Message != nil {
		content := toContent(completion.Message.Content)
		finishReason := toFinishReason(completion.Status, completion.Message.Content)

		result.Candidates = []*Candidate{
			{
				Content:      content,
				FinishReason: finishReason,
				Index:        0,
			},
		}
	}

	writeJson(w, result)
}

func (h *Handler) handleStreamGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")

	completer, messages, options, err := h.parseGenerateRequest(r)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// The genai SDK always uses ?alt=sse for streaming
	// We support both SSE and JSON array formats for compatibility
	useSSE := r.URL.Query().Get("alt") == "sse"

	headersSent := false

	sendHeaders := func() {
		if headersSent {
			return
		}
		// Headers must be set before the first body write — once Write
		// runs, WriteHeader is implicitly called and later Header() edits
		// are silently dropped.
		if useSSE {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		if !useSSE {
			w.Write([]byte("["))
		}
		headersSent = true
	}

	responseID := generateResponseID()
	firstChunk := true

	accumulator := NewStreamingAccumulator(responseID, model, func(response GenerateContentResponse) error {
		err := writeStreamChunk(w, response, useSSE, firstChunk)
		firstChunk = false
		return err
	})

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			if !headersSent {
				writeError(w, http.StatusInternalServerError, err)
				return
			}

			if useSSE {
				writeSSERetry(w, err)
			}
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

	// Emit final chunk with finish reason
	if err := accumulator.Complete(); err != nil {
		if useSSE {
			writeSSERetry(w, err)
		}
		accumulator.Error(err)
		return
	}

	if !useSSE {
		w.Write([]byte("]"))
	}
}

func (h *Handler) parseGenerateRequest(r *http.Request) (provider.Completer, []provider.Message, *provider.CompleteOptions, error) {
	model := r.PathValue("model")

	var req GenerateContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, nil, nil, err
	}

	completer, err := h.Completer(model)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := h.Policy.Verify(r.Context(), policy.ResourceModel, model, policy.ActionAccess); err != nil {
		return nil, nil, nil, err
	}

	messages, err := toMessages(req.SystemInstruction, req.Contents)
	if err != nil {
		return nil, nil, nil, err
	}

	// Per the Gemini spec FunctionCallingConfig.Mode controls function-calling
	// behavior. VALIDATED is equivalent to OpenAI's strict-schema enforcement.
	var fcc *FunctionCallingConfig
	if req.ToolConfig != nil {
		fcc = req.ToolConfig.FunctionCallingConfig
	}
	strict := fcc != nil && fcc.Mode == "VALIDATED"

	options := &provider.CompleteOptions{
		Tools: toTools(req.Tools, strict),
	}

	if fcc != nil {
		toolOptions := &provider.ToolOptions{
			Allowed: fcc.AllowedFunctionNames,
		}
		switch fcc.Mode {
		case "AUTO":
			toolOptions.Choice = provider.ToolChoiceAuto
		case "ANY", "VALIDATED":
			toolOptions.Choice = provider.ToolChoiceAny
		case "NONE":
			toolOptions.Choice = provider.ToolChoiceNone
		}
		if toolOptions.Choice != "" || len(toolOptions.Allowed) > 0 {
			options.ToolOptions = toolOptions
		}
	}

	if req.GenerationConfig != nil {
		options.Stop = req.GenerationConfig.StopSequences
		options.Temperature = req.GenerationConfig.Temperature
		options.MaxTokens = req.GenerationConfig.MaxOutputTokens

		// Handle structured output via responseJsonSchema or responseSchema
		strict := true

		if req.GenerationConfig.ResponseJsonSchema != nil {
			if properties, ok := req.GenerationConfig.ResponseJsonSchema.(map[string]any); ok {
				options.Schema = &provider.Schema{
					Name:       "response",
					Strict:     &strict,
					Properties: properties,
				}
			}
		} else if req.GenerationConfig.ResponseSchema != nil {
			if properties, ok := req.GenerationConfig.ResponseSchema.(map[string]any); ok {
				options.Schema = &provider.Schema{
					Name:       "response",
					Strict:     &strict,
					Properties: properties,
				}
			}
		}
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		tc := req.GenerationConfig.ThinkingConfig

		// The model is asked to think when either includeThoughts is set, an
		// explicit thinkingLevel is provided, or a non-zero thinkingBudget is set.
		wantThinking := tc.IncludeThoughts || tc.ThinkingLevel != "" ||
			(tc.ThinkingBudget != nil && *tc.ThinkingBudget != 0)

		if wantThinking {
			options.ReasoningOptions = &provider.ReasoningOptions{
				IncludeSummary: tc.IncludeThoughts,
			}

			switch strings.TrimPrefix(strings.ToLower(tc.ThinkingLevel), "thinking_level_") {
			case "minimal":
				options.ReasoningOptions.Effort = provider.EffortMinimal
			case "low":
				options.ReasoningOptions.Effort = provider.EffortLow
			case "medium":
				options.ReasoningOptions.Effort = provider.EffortMedium
			case "high":
				options.ReasoningOptions.Effort = provider.EffortHigh
			default:
				options.ReasoningOptions.Effort = effortFromBudget(tc.ThinkingBudget)
			}
		}
	}

	return completer, messages, options, nil
}

// effortFromBudget maps Gemini's numeric thinkingBudget (token allowance) to
// the provider's coarser Effort scale. -1 is the documented "let the model
// decide" sentinel; 0 disables thinking.
func effortFromBudget(budget *int) provider.Effort {
	if budget == nil {
		return provider.EffortMedium
	}
	switch {
	case *budget < 0:
		return provider.EffortMedium
	case *budget == 0:
		return provider.EffortNone
	case *budget <= 1024:
		return provider.EffortMinimal
	case *budget <= 4096:
		return provider.EffortLow
	case *budget <= 16384:
		return provider.EffortMedium
	default:
		return provider.EffortHigh
	}
}

func writeStreamChunk(w http.ResponseWriter, response GenerateContentResponse, useSSE, firstChunk bool) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}

	if useSSE {
		// SSE format: data: {json}\n\n
		if _, err := w.Write([]byte("data: ")); err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n\n")); err != nil {
			return err
		}
	} else {
		// JSON array format: [{...},\n{...}]
		if !firstChunk {
			if _, err := w.Write([]byte(",\n")); err != nil {
				return err
			}
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}

	return http.NewResponseController(w).Flush()
}
