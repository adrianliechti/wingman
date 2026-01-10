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

	var req GenerateContentRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.SystemInstruction, req.Contents)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools := toTools(req.Tools)

	options := &provider.CompleteOptions{
		Tools: tools,
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

	var req GenerateContentRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.SystemInstruction, req.Contents)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools := toTools(req.Tools)

	options := &provider.CompleteOptions{
		Tools: tools,
	}

	if req.GenerationConfig != nil {
		options.Stop = req.GenerationConfig.StopSequences
		options.Temperature = req.GenerationConfig.Temperature
		options.MaxTokens = req.GenerationConfig.MaxOutputTokens

		// Handle structured output
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

	// Check if client requested SSE format via ?alt=sse query parameter
	useSSE := r.URL.Query().Get("alt") == "sse"

	if useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	responseID := generateResponseID()

	accumulator := NewStreamingAccumulator(responseID, model, func(response GenerateContentResponse) error {
		return writeStreamChunk(w, response, useSSE)
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
}

func writeStreamChunk(w http.ResponseWriter, response GenerateContentResponse, useSSE bool) error {
	rc := http.NewResponseController(w)

	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.Encode(response)

	if useSSE {
		// SSE format: data:{json}\n\n
		if _, err := fmt.Fprintf(w, "data:%s\n\n", strings.TrimSpace(data.String())); err != nil {
			return err
		}
	} else {
		if _, err := w.Write(data.Bytes()); err != nil {
			return err
		}
	}

	if err := rc.Flush(); err != nil {
		return err
	}

	return nil
}
