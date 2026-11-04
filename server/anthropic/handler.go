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

func writeError(w http.ResponseWriter, code int, err error) {
	// Use real status code from upstream provider if available
	code = provider.CodeFromError(err, code)

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}
	requestID := errorRequestID(err)
	if requestID != "" {
		w.Header().Set("Request-Id", requestID)
	}

	resp := ErrorResponse{
		Type: "error",

		Error:     responseError(code, err),
		RequestID: requestID,
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

// writeSSERetry emits an SSE "retry:" field (milliseconds) if the error
// carries a RetryAfter duration. Must be written before the event/data
// lines of the SSE message block it belongs to.
func writeSSERetry(w http.ResponseWriter, err error) {
	d := provider.RetryAfterFromError(err)
	if d <= 0 {
		return
	}

	ms := max(d.Milliseconds(), 1)

	fmt.Fprintf(w, "retry: %d\n", ms)
}
