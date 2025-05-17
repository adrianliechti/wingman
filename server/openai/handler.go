package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config
	http.Handler
}

func New(cfg *config.Config) (*Handler, error) {
	mux := chi.NewMux()

	h := &Handler{
		Config:  cfg,
		Handler: mux,
	}

	h.Attach(mux)
	return h, nil
}

func (h *Handler) Attach(r chi.Router) {
	r.Get("/models", h.handleModels)
	r.Get("/models/{id}", h.handleModel)

	r.Post("/embeddings", h.handleEmbeddings)

	r.Post("/chat/completions", h.handleChatCompletion)

	r.Post("/audio/speech", h.handleAudioSpeech)
	r.Post("/audio/transcriptions", h.handleAudioTranscription)

	r.Post("/images/generations", h.handleImageGeneration)
	r.Post("/images/edits", h.handleImageEdit)
}

func writeJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	errorType := "invalid_request_error"

	if code >= 500 {
		errorType = "internal_server_error"
	}

	resp := ErrorResponse{
		Error: Error{
			Type:    errorType,
			Message: err.Error(),
		},
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(resp)
}

func writeEventData(w http.ResponseWriter, v any) error {
	rc := http.NewResponseController(w)

	var data bytes.Buffer

	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.Encode(v)

	event := strings.TrimSpace(data.String())

	if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
		return err
	}

	if err := rc.Flush(); err != nil {
		return err
	}

	return nil
}
