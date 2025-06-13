package openai

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req ResponseRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	completer, err := h.Completer(req.Model)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	messages, err := convertInput(req.Input)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	options := &provider.CompleteOptions{}

	completion, err := completer.Complete(r.Context(), messages, options)

	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result := Response{
		ID: completion.ID,

		Object: "response",

		CreatedAt: time.Now().Unix(),
		Status:    "completed",

		Model: completion.Model,

		Output: ResponseOutput{
			Message: &ResponseMessage{
				Type: "message",
				Role: "assistant",

				Content: ResponseInputContent{
					{
						Type: "output_text",
						ResponseInputText: &ResponseInputText{
							Text: &completion.Message.Content[0].Text,
						},
					},
				},
			},
		},
	}

	writeJson(w, result)
}

func convertInput(input ResponseInput) ([]provider.Message, error) {
	result := make([]provider.Message, 0)

	for _, m := range input.Messages {
		message := provider.Message{
			Role: toMessageRole(m.Role),
		}

		for _, c := range m.Content {
			if c.ResponseInputText != nil {
				message.Content = append(message.Content, provider.Content{
					Text: *c.Text,
				})
			}

			if c.ResponseInputImage != nil {
				file, err := toFile(*c.ImageURL)

				if err != nil {
					return nil, err
				}

				message.Content = append(message.Content, provider.FileContent(file))
			}

			if c.ResponseInputFile != nil {
				file, err := toFile(*c.FileData)

				if err != nil {
					return nil, err
				}

				message.Content = append(message.Content, provider.FileContent(file))
			}
		}

		result = append(result, message)
	}

	return result, nil
}
