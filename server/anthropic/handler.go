package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	// Use real status code from upstream provider if available
	code = provider.CodeFromError(err, code)

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	resp := ErrorResponse{
		Type: "error",

		Error: Error{
			Type:    errorTypeForStatus(code),
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
