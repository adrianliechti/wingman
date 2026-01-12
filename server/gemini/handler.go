package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/config"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	*config.Config
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,
	}
}

func (h *Handler) Attach(r chi.Router) {
	r.Post("/models/{model}:generateContent", h.handleGenerateContent)
	r.Post("/models/{model}:streamGenerateContent", h.handleStreamGenerateContent)
	r.Post("/models/{model}:countTokens", h.handleCountTokens)
}

func writeJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	if err != nil {
		println("server error", err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	var status string
	switch code {
	case http.StatusBadRequest:
		status = "INVALID_ARGUMENT"

	case http.StatusUnauthorized:
		status = "UNAUTHENTICATED"

	case http.StatusForbidden:
		status = "PERMISSION_DENIED"

	case http.StatusNotFound:
		status = "NOT_FOUND"

	case http.StatusTooManyRequests:
		status = "RESOURCE_EXHAUSTED"
	default:
		status = "INTERNAL"

		if code >= 400 && code < 500 {
			status = "INVALID_ARGUMENT"
		}
	}

	resp := ErrorResponse{
		Error: &APIError{
			Code: code,

			Status:  status,
			Message: err.Error(),
		},
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(resp)
}
