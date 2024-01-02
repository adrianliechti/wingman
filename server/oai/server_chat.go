package oai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/adrianliechti/llama/pkg/provider"

	"github.com/google/uuid"
)

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatCompletionRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	completer, err := s.Completer(req.Model)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	id := uuid.New().String()

	//model := req.Model
	messages := toMessages(req.Messages)

	options := &provider.CompleteOptions{
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	if req.Format != nil {
		if req.Format.Type == ResponseFormatJSON {
			options.Format = provider.CompletionFormatJSON
		}
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		done := make(chan error)
		stream := make(chan provider.Completion)

		go func() {
			options.Stream = stream

			_, err := completer.Complete(r.Context(), messages, options)
			done <- err
		}()

		for completion := range stream {
			result := ChatCompletion{
				Object: "chat.completion.chunk",

				ID: id,

				Model:   req.Model,
				Created: time.Now().Unix(),

				Choices: []ChatCompletionChoice{
					{
						Delta: &ChatCompletionMessage{
							//Role:    fromMessageRole(completion.Role),
							Content: completion.Content,
						},

						FinishReason: fromCompletionReason(completion.Reason),
					},
				},
			}

			var data bytes.Buffer

			enc := json.NewEncoder(&data)
			enc.SetEscapeHTML(false)
			enc.Encode(result)

			fmt.Fprintf(w, "data: %s\n\n", data.String())
			w.(http.Flusher).Flush()
		}

		// fmt.Fprintf(w, "data: [DONE]\n\n")
		// w.(http.Flusher).Flush()

		if err := <-done; err != nil {
			slog.Error("error in chat completion", "error", err)
		}

	} else {
		completion, err := completer.Complete(r.Context(), messages, options)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		result := ChatCompletion{
			Object: "chat.completion",

			ID: id,

			Model:   req.Model,
			Created: time.Now().Unix(),

			Choices: []ChatCompletionChoice{
				{
					Message: &ChatCompletionMessage{
						Role:    fromMessageRole(completion.Role),
						Content: completion.Content,
					},

					FinishReason: fromCompletionReason(completion.Reason),
				},
			},
		}

		writeJson(w, result)
	}
}
