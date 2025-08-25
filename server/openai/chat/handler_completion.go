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

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/shared"

	"github.com/google/uuid"
)

func (h *Handler) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var req openai.ChatCompletionNewParams

	if err := json.Unmarshal(data, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var mode struct {
		Stream bool `json:"stream"`
	}

	if err := json.Unmarshal(data, &mode); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tools, err := toTools(req.Tools)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Messages)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var stops []string

	if len(req.Stop.OfStringArray) > 0 {
		stops = req.Stop.OfStringArray
	} else if req.Stop.OfString.Valid() {
		stops = []string{req.Stop.OfString.Value}
	}

	options := &provider.CompleteOptions{
		Stop:  stops,
		Tools: tools,
	}

	if req.MaxTokens.Valid() {
		val := int(req.MaxTokens.Value)
		options.MaxTokens = &val
	}

	if req.Temperature.Valid() {
		val := float32(req.Temperature.Value)
		options.Temperature = &val
	}

	switch req.ReasoningEffort {
	case shared.ReasoningEffortMinimal:
		options.Effort = provider.EffortMinimal

	case shared.ReasoningEffortLow:
		options.Effort = provider.EffortLow

	case shared.ReasoningEffortMedium:
		options.Effort = provider.EffortMedium

	case shared.ReasoningEffortHigh:
		options.Effort = provider.EffortHigh
	}

	if req.ResponseFormat.OfJSONObject != nil {
		options.Format = provider.CompletionFormatJSON
	}

	if req.ResponseFormat.OfJSONSchema != nil {
		options.Format = provider.CompletionFormatJSON

		schema := req.ResponseFormat.OfJSONSchema.JSONSchema

		options.Schema = &provider.Schema{
			Name: schema.Name,
		}

		if schema.Schema != nil {
			var val map[string]any

			data, _ := json.Marshal(schema.Schema)
			json.Unmarshal(data, &val)

			options.Schema.Schema = tool.NormalizeSchema(val)
		}

		if schema.Strict.Valid() {
			val := schema.Strict.Value
			options.Schema.Strict = &val
		}

		if schema.Description.Valid() {
			val := schema.Description.Value
			options.Schema.Description = val
		}
	}

	if mode.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var role provider.MessageRole
		var reason provider.CompletionReason

		completionCall := ""
		completionCallIndex := map[string]int{}

		options.Stream = func(ctx context.Context, completion provider.Completion) error {
			if completion.Usage != nil && (completion.Message == nil || len(completion.Message.Content) == 0) {
				return nil
			}

			result := openai.ChatCompletionChunk{
				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			if completion.Message != nil {
				message := openai.ChatCompletionChunkChoiceDelta{}

				if completion.Message.Role != role {
					role = completion.Message.Role

					switch role {
					case provider.MessageRoleAssistant:
						message.Role = "assistant"
					}
				}

				if content := completion.Message.Text(); content != "" {
					message.Content = content
				}

				if calls := oaiDeltaToolCalls(completion.Message.Content); len(calls) > 0 {
					message.Content = ""

					for i, c := range calls {
						if c.ID != "" {
							completionCall = c.ID
							completionCallIndex[completionCall] = len(completionCallIndex)
						}

						if completionCall == "" {
							continue
						}

						call := openai.ChatCompletionChunkChoiceDeltaToolCall{
							ID:    c.ID,
							Index: int64(len(completionCallIndex) - 1),

							Type:     c.Type,
							Function: c.Function,
						}

						calls[i] = call
					}

					message.ToolCalls = calls
				}

				result.Choices = []openai.ChatCompletionChunkChoice{
					{
						Delta: message,
					},
				}
			}

			if completion.Reason != "" {
				reason = completion.Reason
			}

			return writeEventData(w, result)
		}

		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		if reason == "" {
			reason = completion.Reason

			if reason == "" {
				reason = provider.CompletionReasonStop

				if completion.Message != nil {
					if len(oaiToolCalls(completion.Message.Content)) > 0 {
						reason = provider.CompletionReasonTool
					}
				}
			}

			result := openai.ChatCompletionChunk{
				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),

				Choices: []openai.ChatCompletionChunkChoice{
					{
						FinishReason: string(oaiFinishReason(reason)),
					},
				},
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			writeEventData(w, result)
		}

		if req.StreamOptions.IncludeUsage.Value && completion.Usage != nil {
			result := openai.ChatCompletionChunk{
				ID: completion.ID,

				Model:   completion.Model,
				Created: time.Now().Unix(),

				Choices: []openai.ChatCompletionChunkChoice{
					{
						Delta: openai.ChatCompletionChunkChoiceDelta{},
					},
				},
			}

			if result.Model == "" {
				result.Model = req.Model
			}

			result.Usage = openai.CompletionUsage{
				PromptTokens:     int64(completion.Usage.InputTokens),
				CompletionTokens: int64(completion.Usage.OutputTokens),
				TotalTokens:      int64(completion.Usage.InputTokens + completion.Usage.OutputTokens),
			}

			writeEventData(w, result)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		result := openai.ChatCompletion{
			ID: completion.ID,

			Model:   completion.Model,
			Created: time.Now().Unix(),
		}

		if result.Model == "" {
			result.Model = req.Model
		}

		if completion.Message != nil {
			reason := oaiFinishReason(completion.Reason)

			message := openai.ChatCompletionMessage{}

			if content := completion.Message.Text(); content != "" {
				message.Content = content
			}

			if calls := oaiToolCalls(completion.Message.Content); len(calls) > 0 {
				reason = openai.CompletionChoiceFinishReason("tool")

				message.Content = ""
				message.ToolCalls = calls
			}

			result.Choices = []openai.ChatCompletionChoice{
				{
					Message:      message,
					FinishReason: string(reason),
				},
			}
		}

		if completion.Usage != nil {
			result.Usage = openai.CompletionUsage{
				PromptTokens:     int64(completion.Usage.InputTokens),
				CompletionTokens: int64(completion.Usage.OutputTokens),
				TotalTokens:      int64(completion.Usage.InputTokens + completion.Usage.OutputTokens),
			}
		}

		writeJson(w, result)
	}
}

func toMessages(messages []openai.ChatCompletionMessageParamUnion) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for _, m := range messages {

		if m := m.OfUser; m != nil {
			message := provider.Message{
				Role: provider.MessageRoleUser,
			}

			if m.Content.OfString.Valid() {
				content := provider.TextContent(m.Content.OfString.Value)
				message.Content = append(message.Content, content)
			}

			for _, c := range m.Content.OfArrayOfContentParts {
				if c.OfText != nil {
					content := provider.TextContent(c.OfText.Text)
					message.Content = append(message.Content, content)
				}

				if c.OfImageURL != nil {
					image := c.OfImageURL.ImageURL

					file, err := toFile(image.URL)

					if err != nil {
						return nil, err
					}

					message.Content = append(message.Content, provider.FileContent(file))
				}

				if c.OfInputAudio != nil {
					input := c.OfInputAudio.InputAudio

					data, err := base64.StdEncoding.DecodeString(input.Data)

					if err != nil {
						return nil, err
					}

					file := &provider.File{
						Content: data,
					}

					if input.Format != "" {
						file.Name = uuid.NewString() + "." + input.Format
					}

					message.Content = append(message.Content, provider.FileContent(file))
				}

				if c.OfFile != nil {
					input := c.OfFile.File

					file, err := toFile(input.FileData.Value)

					if err != nil {
						return nil, err
					}

					if input.Filename.Valid() {
						file.Name = input.Filename.Value
					}

					message.Content = append(message.Content, provider.FileContent(file))
				}
			}

			result = append(result, message)
		}

		if m := m.OfAssistant; m != nil {
			message := provider.Message{
				Role: provider.MessageRoleAssistant,
			}

			if m.Content.OfString.Valid() {
				content := provider.TextContent(m.Content.OfString.Value)
				message.Content = append(message.Content, content)
			}

			for _, c := range m.Content.OfArrayOfContentParts {
				if c.OfText != nil {
					content := provider.TextContent(c.OfText.Text)
					message.Content = append(message.Content, content)
				}

				if c.OfRefusal != nil {
					content := provider.TextContent(c.OfRefusal.Refusal)
					message.Content = append(message.Content, content)
				}
			}

			for _, c := range m.ToolCalls {
				if c := c.OfFunction; c != nil {
					call := provider.ToolCall{
						ID: c.ID,

						Name:      c.Function.Name,
						Arguments: c.Function.Arguments,
					}

					message.Content = append(message.Content, provider.ToolCallContent(call))
				}
			}

			result = append(result, message)
		}

		if m := m.OfSystem; m != nil {
			message := provider.Message{
				Role: provider.MessageRoleSystem,
			}

			if m.Content.OfString.Valid() {
				content := provider.TextContent(m.Content.OfString.Value)
				message.Content = append(message.Content, content)
			}

			for _, c := range m.Content.OfArrayOfContentParts {
				content := provider.TextContent(c.Text)
				message.Content = append(message.Content, content)
			}

			result = append(result, message)
		}

		if m := m.OfDeveloper; m != nil {
			message := provider.Message{
				Role: provider.MessageRoleSystem,
			}

			if m.Content.OfString.Valid() {
				content := provider.TextContent(m.Content.OfString.Value)
				message.Content = append(message.Content, content)
			}

			for _, c := range m.Content.OfArrayOfContentParts {
				content := provider.TextContent(c.Text)
				message.Content = append(message.Content, content)
			}

			result = append(result, message)
		}

		if m := m.OfTool; m != nil {
			message := provider.Message{
				Role: provider.MessageRoleUser,
			}

			content := provider.ToolResult{
				ID:   m.ToolCallID,
				Data: m.Content.OfString.Value,
			}

			message.Content = append(message.Content, provider.ToolResultContent(content))

			result = append(result, message)
		}
	}

	return result, nil
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

		file := provider.File{
			Content:     data,
			ContentType: resp.Header.Get("Content-Type"),
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

func toTools(tools []openai.ChatCompletionToolUnionParam) ([]provider.Tool, error) {
	var result []provider.Tool

	for _, t := range tools {
		if t := t.OfFunction; t != nil {
			tool := provider.Tool{
				Name: t.Function.Name,

				Parameters: tool.NormalizeSchema(t.Function.Parameters),
			}

			if t.Function.Description.Valid() {
				tool.Description = t.Function.Description.Value
			}

			result = append(result, tool)
		}
	}

	return result, nil
}

func oaiFinishReason(val provider.CompletionReason) openai.CompletionChoiceFinishReason {
	switch val {
	case provider.CompletionReasonStop:
		return openai.CompletionChoiceFinishReasonStop

	case provider.CompletionReasonLength:
		return openai.CompletionChoiceFinishReasonLength

	case provider.CompletionReasonFilter:
		return openai.CompletionChoiceFinishReasonContentFilter

	default:
		return ""
	}
}

func oaiToolCalls(content []provider.Content) []openai.ChatCompletionMessageToolCallUnion {
	var result []openai.ChatCompletionMessageToolCallUnion

	for _, c := range content {
		if c.ToolCall == nil {
			continue
		}

		call := openai.ChatCompletionMessageToolCallUnion{
			ID: c.ToolCall.ID,

			Type: "function",

			Function: openai.ChatCompletionMessageFunctionToolCallFunction{
				Name:      c.ToolCall.Name,
				Arguments: c.ToolCall.Arguments,
			},
		}

		result = append(result, call)
	}

	return result
}

func oaiDeltaToolCalls(content []provider.Content) []openai.ChatCompletionChunkChoiceDeltaToolCall {
	var result []openai.ChatCompletionChunkChoiceDeltaToolCall

	for _, c := range content {
		if c.ToolCall == nil {
			continue
		}

		call := openai.ChatCompletionChunkChoiceDeltaToolCall{
			ID: c.ToolCall.ID,

			Type: "function",

			Function: openai.ChatCompletionChunkChoiceDeltaToolCallFunction{
				Name:      c.ToolCall.Name,
				Arguments: c.ToolCall.Arguments,
			},
		}

		result = append(result, call)
	}

	return result
}
