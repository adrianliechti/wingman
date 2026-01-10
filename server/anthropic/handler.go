package anthropic

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
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		Config: cfg,
	}
}

func (h *Handler) Attach(r chi.Router) {
	r.Post("/messages", h.handleMessages)
	r.Post("/messages/count_tokens", h.handleCountTokens)
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

	var errorType string
	switch code {
	case http.StatusUnauthorized:
		errorType = "authentication_error"

	case http.StatusForbidden:
		errorType = "permission_error"

	case http.StatusNotFound:
		errorType = "not_found_error"

	case http.StatusTooManyRequests:
		errorType = "rate_limit_error"

	default:
		errorType = "invalid_request_error"

		if code >= 500 {
			errorType = "api_error"
		}
	}

	resp := ErrorResponse{
		Type: "error",

		Error: Error{
			Type:    errorType,
			Message: err.Error(),
		},
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(resp)
}

func writeEvent(w http.ResponseWriter, eventType string, v any) error {
	rc := http.NewResponseController(w)

	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.Encode(v)

	event := strings.TrimSpace(data.String())

	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, event); err != nil {
		return err
	}

	if err := rc.Flush(); err != nil {
		return err
	}

	return nil
}
