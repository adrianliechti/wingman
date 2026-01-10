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

	errorType := "invalid_request_error"

	if code == http.StatusUnauthorized {
		errorType = "authentication_error"
	} else if code == http.StatusForbidden {
		errorType = "permission_error"
	} else if code == http.StatusNotFound {
		errorType = "not_found_error"
	} else if code == http.StatusTooManyRequests {
		errorType = "rate_limit_error"
	} else if code >= 500 {
		errorType = "api_error"
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
