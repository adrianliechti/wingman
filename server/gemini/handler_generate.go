package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

	if completion.Usage != nil {
		result.UsageMetadata = &UsageMetadata{
			PromptTokenCount:     completion.Usage.InputTokens,
			CandidatesTokenCount: completion.Usage.OutputTokens,
			TotalTokenCount:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
		}
	}

	if completion.Message != nil {
		content := toContent(completion.Message.Content)
		finishReason := toFinishReason(completion.Message.Content)

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

	if useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("["))
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	responseID := generateResponseID()
	firstChunk := true

	accumulator := NewStreamingAccumulator(responseID, model, func(response GenerateContentResponse) error {
		err := writeStreamChunk(w, response, useSSE, firstChunk)
		firstChunk = false
		return err
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

	// Emit final chunk with finish reason
	if err := accumulator.Complete(); err != nil {
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

	messages, err := toMessages(req.SystemInstruction, req.Contents)
	if err != nil {
		return nil, nil, nil, err
	}

	options := &provider.CompleteOptions{
		Tools: toTools(req.Tools),
	}

	if req.GenerationConfig != nil {
		options.Stop = req.GenerationConfig.StopSequences
		options.Temperature = req.GenerationConfig.Temperature
		options.MaxTokens = req.GenerationConfig.MaxOutputTokens

		// Handle structured output via responseJsonSchema or responseSchema
		if req.GenerationConfig.ResponseJsonSchema != nil {
			if schema, ok := req.GenerationConfig.ResponseJsonSchema.(map[string]any); ok {
				options.Schema = &provider.Schema{
					Name:   "response",
					Schema: schema,
				}
			}
		} else if req.GenerationConfig.ResponseSchema != nil {
			if schema, ok := req.GenerationConfig.ResponseSchema.(map[string]any); ok {
				options.Schema = &provider.Schema{
					Name:   "response",
					Schema: schema,
				}
			}
		}
	}

	return completer, messages, options, nil
}

func writeStreamChunk(w http.ResponseWriter, response GenerateContentResponse, useSSE, firstChunk bool) error {
	rc := http.NewResponseController(w)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(response)

	data := strings.TrimSpace(buf.String())

	if useSSE {
		// SSE format: data: {json}\r\n\r\n
		if _, err := fmt.Fprintf(w, "data: %s\r\n\r\n", data); err != nil {
			return err
		}
	} else {
		// JSON array format: [{...},\n{...}]
		if !firstChunk {
			if _, err := w.Write([]byte(",\n")); err != nil {
				return err
			}
		}
		if _, err := w.Write([]byte(data)); err != nil {
			return err
		}
	}

	return rc.Flush()
}
