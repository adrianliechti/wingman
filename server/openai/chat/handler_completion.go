package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"

	"github.com/google/uuid"
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

		MaxTokens:   req.MaxTokens,
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
		if req.ResponseFormat.Type == ResponseFormatJSONObject || req.ResponseFormat.Type == ResponseFormatJSONSchema {
			options.Format = provider.CompletionFormatJSON
		}

		if req.ResponseFormat.JSONSchema != nil {
			options.Format = provider.CompletionFormatJSON

			options.Schema = &provider.Schema{
				Name:        req.ResponseFormat.JSONSchema.Name,
				Description: req.ResponseFormat.JSONSchema.Description,

				Strict: req.ResponseFormat.JSONSchema.Strict,
				Schema: req.ResponseFormat.JSONSchema.Schema,
			}
		}
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		streamRole := true
		streamReason := FinishReasonStop

		completionCall := ""
		completionCallIndex := map[string]int{}

		options.Stream = func(ctx context.Context, completion provider.Completion) error {
			if completion.Usage != nil && (completion.Message == nil || len(completion.Message.Content) == 0) {
				return nil
			}

			result := ChatCompletion{
				Object: "chat.completion.chunk",

				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),

				Choices: []ChatCompletionChoice{
					{
						Delta: &ChatCompletionMessage{},
					},
				},
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			if completion.Message != nil {
				message := &ChatCompletionMessage{}

				if content := completion.Message.Text(); content != "" {
					message.Content = &content
				}

				if calls := oaiToolCalls(completion.Message.Content); len(calls) > 0 {
					for i, c := range calls {
						if c.ID != "" {
							completionCall = c.ID
							completionCallIndex[completionCall] = len(completionCallIndex)
						}

						if completionCall == "" {
							continue
						}

						call := ToolCall{
							ID:    completionCall,
							Index: completionCallIndex[completionCall],

							Type:     c.Type,
							Function: c.Function,
						}

						calls[i] = call
					}

					streamReason = FinishReasonToolCalls

					message.Content = nil
					message.ToolCalls = calls
				}

				result.Choices = []ChatCompletionChoice{
					{
						Delta: message,
					},
				}
			}

			if streamRole {
				streamRole = false
				result.Choices[0].Delta.Role = MessageRoleAssistant
			}

			return writeEvent(w, result)
		}

		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		if streamReason != "" {
			result := ChatCompletion{
				Object: "chat.completion.chunk",

				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),

				Choices: []ChatCompletionChoice{
					{
						Delta:        &ChatCompletionMessage{},
						FinishReason: &streamReason,
					},
				},
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			writeEvent(w, result)
		}

		if streamUsage(req) && completion.Usage != nil {
			result := ChatCompletion{
				Object: "chat.completion.chunk",

				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),

				Choices: []ChatCompletionChoice{},
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			result.Usage = &Usage{
				PromptTokens:     completion.Usage.InputTokens,
				CompletionTokens: completion.Usage.OutputTokens,
				TotalTokens:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
			}

			writeEvent(w, result)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

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
}

func streamUsage(req ChatCompletionRequest) bool {
	if req.StreamOptions == nil {
		return false
	}

	if req.StreamOptions.IncludeUsage == nil {
		return false
	}

	return *req.StreamOptions.IncludeUsage
}

func toMessages(s []ChatCompletionMessage) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for _, m := range s {
		role := toMessageRole(m.Role)

		if role == "" {
			continue
		}

		var content []provider.Content

		if len(m.Contents) == 0 {
			if m.ToolCallID != "" {
				result := provider.ToolResult{
					ID: m.ToolCallID,
				}

				if m.Content != nil {
					result.Data = *m.Content
				}

				content = append(content, provider.ToolResultContent(result))
			} else if m.Content != nil {
				content = append(content, provider.TextContent(*m.Content))
			}
		}

		for _, c := range m.Contents {
			if c.Type == MessageContentTypeText {
				content = append(content, provider.TextContent(c.Text))
			}

			if c.Type == MessageContentTypeFile && c.File != nil {
				file, err := toFile(c.File.Data)

				if err != nil {
					return nil, err
				}

				if c.File.Name != "" {
					file.Name = c.File.Name
				}

				content = append(content, provider.FileContent(file))
			}

			if c.Type == MessageContentTypeImage && c.Image != nil {
				file, err := toFile(c.Image.URL)

				if err != nil {
					return nil, err
				}

				content = append(content, provider.FileContent(file))
			}

			if c.Type == MessageContentTypeAudio && c.Audio != nil {
				data, err := base64.StdEncoding.DecodeString(c.Audio.Data)

				if err != nil {
					return nil, err
				}

				file := &provider.File{
					Content: data,
				}

				if c.Audio.Format != "" {
					file.Name = uuid.NewString() + c.Audio.Format
				}

				content = append(content, provider.FileContent(file))
			}
		}

		for _, c := range m.ToolCalls {
			if c.Type == ToolTypeFunction && c.Function != nil {
				call := provider.ToolCall{
					ID: c.ID,

					Name:      c.Function.Name,
					Arguments: c.Function.Arguments,
				}

				content = append(content, provider.ToolCallContent(call))
			}
		}

		result = append(result, provider.Message{
			Role:    role,
			Content: content,
		})
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem, MessageRoleDeveloper:
		return provider.MessageRoleSystem

	case MessageRoleUser, MessageRoleTool:
		return provider.MessageRoleUser

	case MessageRoleAssistant:
		return provider.MessageRoleAssistant

	default:
		return ""
	}
}

func toFile(url string) (*provider.File, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		resp, err := http.Get(url)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)

		if err != nil {
			return nil, err
		}

		contentType := resp.Header.Get("Content-Type")

		if mediatype, _, err := mime.ParseMediaType(contentType); err == nil {
			contentType = mediatype
		}

		file := provider.File{
			Content:     data,
			ContentType: contentType,
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	if strings.HasPrefix(url, "data:") {
		re := regexp.MustCompile(`data:([a-zA-Z]+\/[a-zA-Z0-9.+_-]+);base64,\s*(.+)`)

		match := re.FindStringSubmatch(url)

		if len(match) != 3 {
			return nil, fmt.Errorf("invalid data url")
		}

		data, err := base64.StdEncoding.DecodeString(match[2])

		if err != nil {
			return nil, fmt.Errorf("invalid data encoding")
		}

		file := provider.File{
			Content:     data,
			ContentType: match[1],
		}

		if ext, _ := mime.ExtensionsByType(file.ContentType); len(ext) > 0 {
			file.Name = uuid.New().String() + ext[0]
		}

		return &file, nil
	}

	return nil, fmt.Errorf("invalid url")
}

func toTools(tools []Tool) ([]provider.Tool, error) {
	var result []provider.Tool

	for _, t := range tools {
		if t.Type == ToolTypeFunction && t.ToolFunction != nil {
			function := provider.Tool{
				Name:        t.ToolFunction.Name,
				Description: t.ToolFunction.Description,

				Parameters: tool.NormalizeSchema(t.ToolFunction.Parameters),
			}

			result = append(result, function)
		}
	}

	return result, nil
}

func oaiToolCalls(content []provider.Content) []ToolCall {
	result := make([]ToolCall, 0)

	for _, c := range content {
		if c.ToolCall == nil {
			continue
		}

		call := ToolCall{
			ID:    c.ToolCall.ID,
			Index: len(result),

			Type: ToolTypeFunction,

			Function: &FunctionCall{
				Name:      c.ToolCall.Name,
				Arguments: c.ToolCall.Arguments,
			},
		}

		result = append(result, call)
	}

	return result
}
