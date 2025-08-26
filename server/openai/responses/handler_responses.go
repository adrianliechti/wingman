package responses

import (
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
	"github.com/google/uuid"
)

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req ResponsesRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := toMessages(req.Input.Messages)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.CompleteOptions{
		//Stop:  stops,
		//Tools: tools,

		//MaxTokens:   req.MaxTokens,
		//Temperature: req.Temperature,
	}

	if req.Stream {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("streaming not implemented"))
	} else {
		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		result := Response{
			Object: "response",
			Status: "completed",

			ID: completion.ID,

			Model:     completion.Model,
			CreatedAt: time.Now().Unix(),

			Output: []ResponseOutput{},
		}

		if result.Model == "" {
			result.Model = req.Model
		}

		if completion.Message != nil {
			output := ResponseOutput{
				Type: ResponseOutputTypeMessage,

				OutputMessage: &OutputMessage{
					Role: "assistant",

					Status: "completed",

					Contents: []OutputContent{
						{
							Type: "output_text",
							Text: completion.Message.Text(),
						},
					},
				},
			}

			result.Output = append(result.Output, output)
		}

		writeJson(w, result)
	}
}

func toMessages(s []InputMessage) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for _, m := range s {
		var content []provider.Content

		for _, c := range m.Content {
			if c.Type == InputContentText {
				content = append(content, provider.TextContent(c.Text))
			}

			// if c.Type == InputContentFile && c.File != nil {
			// 	file, err := toFile(c.File.Data)

			// 	if err != nil {
			// 		return nil, err
			// 	}

			// 	if c.File.Name != "" {
			// 		file.Name = c.File.Name
			// 	}

			// 	content = append(content, provider.FileContent(file))
			// }

			if c.Type == InputContentImage && c.ImageURL != "" {
				file, err := toFile(c.ImageURL)

				if err != nil {
					return nil, err
				}

				content = append(content, provider.FileContent(file))
			}

			// if c.Type == MessageContentTypeAudio && c.Audio != nil {
			// 	data, err := base64.StdEncoding.DecodeString(c.Audio.Data)

			// 	if err != nil {
			// 		return nil, err
			// 	}

			// 	file := &provider.File{
			// 		Content: data,
			// 	}

			// 	if c.Audio.Format != "" {
			// 		file.Name = uuid.NewString() + c.Audio.Format
			// 	}

			// 	content = append(content, provider.FileContent(file))
			// }
		}

		// for _, c := range m.ToolCalls {
		// 	if c.Type == ToolTypeFunction && c.Function != nil {
		// 		call := provider.ToolCall{
		// 			ID: c.ID,

		// 			Name:      c.Function.Name,
		// 			Arguments: c.Function.Arguments,
		// 		}

		// 		content = append(content, provider.ToolCallContent(call))
		// 	}
		// }

		result = append(result, provider.Message{
			Role:    toMessageRole(m.Role),
			Content: content,
		})
	}

	return result, nil
}

func toMessageRole(r MessageRole) provider.MessageRole {
	switch r {
	case MessageRoleSystem:
		return provider.MessageRoleSystem

	case MessageRoleUser: // MessageRoleTool
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
