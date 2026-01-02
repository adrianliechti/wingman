package content

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) handleGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")

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

	messages, err := toMessages(req)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := toCompleteOptions(req, tools)

	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		acc.Add(*completion)
	}

	completion := acc.Result()

	result := toGenerateContentResponse(completion, model)

	writeJson(w, result)
}

func (h *Handler) handleStreamGenerateContent(w http.ResponseWriter, r *http.Request) {
	model := chi.URLParam(r, "model")

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

	messages, err := toMessages(req)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := toCompleteOptions(req, tools)

	// Check if SSE is requested via query param
	alt := r.URL.Query().Get("alt")
	isSSE := alt == "sse"

	if isSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}

	for completion, err := range completer.Complete(r.Context(), messages, options) {
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		if completion.Message == nil || len(completion.Message.Content) == 0 {
			continue
		}

		result := toGenerateContentResponse(completion, model)

		if isSSE {
			if err := writeEvent(w, result); err != nil {
				return
			}
		} else {
			writeJson(w, result)
		}
	}
}

func toCompleteOptions(req GenerateContentRequest, tools []provider.Tool) *provider.CompleteOptions {
	options := &provider.CompleteOptions{
		Tools: tools,
	}

	if req.GenerationConfig != nil {
		options.Stop = req.GenerationConfig.StopSequences
		options.MaxTokens = req.GenerationConfig.MaxOutputTokens
		options.Temperature = req.GenerationConfig.Temperature

		if req.GenerationConfig.ResponseMimeType == "application/json" {
			options.Format = provider.CompletionFormatJSON
		}

		if req.GenerationConfig.ResponseSchema != nil {
			options.Format = provider.CompletionFormatJSON

			if schema, ok := req.GenerationConfig.ResponseSchema.(map[string]any); ok {
				options.Schema = &provider.Schema{
					Schema: schema,
				}
			}
		}

		// Map thinking budget to effort
		if req.GenerationConfig.ThinkingConfig != nil {
			if req.GenerationConfig.ThinkingConfig.ThinkingBudget != nil {
				budget := *req.GenerationConfig.ThinkingConfig.ThinkingBudget
				switch {
				case budget == 0:
					options.Effort = provider.EffortMinimal
				case budget <= 1024:
					options.Effort = provider.EffortLow
				case budget <= 8192:
					options.Effort = provider.EffortMedium
				default:
					options.Effort = provider.EffortHigh
				}
			}
		}
	}

	return options
}

func toMessages(req GenerateContentRequest) ([]provider.Message, error) {
	var result []provider.Message

	// Handle system instruction
	if req.SystemInstruction != nil {
		var content []provider.Content

		for _, p := range req.SystemInstruction.Parts {
			if p.Text != "" {
				content = append(content, provider.TextContent(p.Text))
			}
		}

		if len(content) > 0 {
			result = append(result, provider.Message{
				Role:    provider.MessageRoleSystem,
				Content: content,
			})
		}
	}

	// Handle contents
	for _, c := range req.Contents {
		role := toMessageRole(c.Role)

		if role == "" {
			continue
		}

		var content []provider.Content

		for _, p := range c.Parts {
			if p.Text != "" {
				content = append(content, provider.TextContent(p.Text))
			}

			if p.InlineData != nil {
				data, err := base64.StdEncoding.DecodeString(p.InlineData.Data)

				if err != nil {
					return nil, err
				}

				file := &provider.File{
					Content:     data,
					ContentType: p.InlineData.MimeType,
				}

				content = append(content, provider.FileContent(file))
			}

			if p.FunctionCall != nil {
				args, _ := json.Marshal(p.FunctionCall.Args)

				// Format ID as "::name::" so Google provider's parseToolID can extract the name
				id := "::" + p.FunctionCall.Name + "::"

				call := provider.ToolCall{
					ID:        id,
					Name:      p.FunctionCall.Name,
					Arguments: string(args),
				}

				content = append(content, provider.ToolCallContent(call))
			}

			if p.FunctionResponse != nil {
				data, _ := json.Marshal(p.FunctionResponse.Response)

				// Format ID as "::name::" so Google provider's parseToolID can extract the name
				id := "::" + p.FunctionResponse.Name + "::"

				toolResult := provider.ToolResult{
					ID:   id,
					Data: string(data),
				}

				content = append(content, provider.ToolResultContent(toolResult))
			}
		}

		result = append(result, provider.Message{
			Role:    role,
			Content: content,
		})
	}

	return result, nil
}

func toMessageRole(r Role) provider.MessageRole {
	switch r {
	case RoleUser:
		return provider.MessageRoleUser
	case RoleModel:
		return provider.MessageRoleAssistant
	default:
		return provider.MessageRoleUser
	}
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	var result []provider.Tool

	for _, t := range tools {
		for _, f := range t.FunctionDeclarations {
			function := provider.Tool{
				Name:        f.Name,
				Description: f.Description,
				Parameters:  tool.NormalizeSchema(f.Parameters),
			}

			result = append(result, function)
		}
	}

	return result, nil
}

func toGenerateContentResponse(completion *provider.Completion, model string) *GenerateContentResponse {
	result := &GenerateContentResponse{
		ModelVersion: model,
	}

	if completion.Message != nil {
		candidate := Candidate{
			Index:        0,
			FinishReason: FinishReasonStop,
			Content: &Content{
				Role: RoleModel,
			},
		}

		for _, c := range completion.Message.Content {
			if c.Text != "" {
				candidate.Content.Parts = append(candidate.Content.Parts, Part{
					Text: c.Text,
				})
			}

			if c.ToolCall != nil {
				var args map[string]any
				json.Unmarshal([]byte(c.ToolCall.Arguments), &args)

				candidate.Content.Parts = append(candidate.Content.Parts, Part{
					FunctionCall: &FunctionCall{
						Name: c.ToolCall.Name,
						Args: args,
					},
				})

				candidate.FinishReason = FinishReasonStop
			}
		}

		// Check if there are tool calls
		if len(completion.Message.ToolCalls()) > 0 {
			candidate.FinishReason = FinishReasonStop
		}

		result.Candidates = append(result.Candidates, candidate)
	}

	if completion.Usage != nil {
		result.UsageMetadata = &UsageMetadata{
			PromptTokenCount:     completion.Usage.InputTokens,
			CandidatesTokenCount: completion.Usage.OutputTokens,
			TotalTokenCount:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
		}
	}

	return result
}
