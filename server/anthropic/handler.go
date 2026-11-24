package anthropic

import (
	"encoding/json/v2"
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"

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
	data, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("encode response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func writeError(w http.ResponseWriter, fallback int, err error) {
	code, resp := errorResponse(err, fallback)
	if provider.KindFromError(err) == provider.ErrorKindQuotaExceeded {
		w.Header().Set("x-should-retry", "false")
	}

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}
	if resp.RequestID != "" {
		w.Header().Set("Request-Id", resp.RequestID)
	}

	data, encodeErr := json.Marshal(resp)
	if encodeErr != nil {
		code = http.StatusInternalServerError
		data = []byte(`{"type":"error","error":{"type":"api_error","message":"could not encode error response"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func writeEvent(w http.ResponseWriter, eventType string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
