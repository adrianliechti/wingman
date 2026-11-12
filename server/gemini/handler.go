package gemini

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	r.Post("/decisions", h.handleDecisions)
	r.Post("/models/{model}:generateContent", h.handleGenerateContent)
	r.Post("/models/{model}:streamGenerateContent", h.handleStreamGenerateContent)
	r.Post("/models/{model}:countTokens", h.handleCountTokens)
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
	if err != nil {
		println("server error", err.Error())
	}

	apiError := toAPIError(err, code)

	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}

	data, encodeErr := json.Marshal(ErrorResponse{Error: apiError})
	if encodeErr != nil {
		apiError.Code = http.StatusInternalServerError
		data = []byte(`{"error":{"code":500,"status":"INTERNAL","message":"could not encode error response"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiError.Code)
	w.Write(data)
}

func toAPIError(err error, fallback int) *APIError {
	code := fallback
	if upstream, ok := errors.AsType[*provider.ProviderError](err); ok && upstream.Code >= 400 {
		code = upstream.Code
	}

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

	case http.StatusConflict:
		status = "ABORTED"

	case http.StatusTooManyRequests:
		status = "RESOURCE_EXHAUSTED"
	case 499:
		status = "CANCELLED"
	case http.StatusNotImplemented:
		status = "UNIMPLEMENTED"
	case http.StatusServiceUnavailable, 529:
		code, status = http.StatusServiceUnavailable, "UNAVAILABLE"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code, status = http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"
	default:
		if code >= 500 {
			code = http.StatusInternalServerError
		}
		status = "INTERNAL"

		if code >= 400 && code < 500 {
			status = "INVALID_ARGUMENT"
		}
	}
	// Several Google canonical statuses share an HTTP code. Preserve these
	// distinctions when supplied by a Google provider, never foreign type names.
	switch upstreamType := provider.TypeFromError(err); {
	case code == 400 && (upstreamType == "FAILED_PRECONDITION" || upstreamType == "OUT_OF_RANGE"),
		code == 409 && upstreamType == "ALREADY_EXISTS",
		code == 500 && (upstreamType == "UNKNOWN" || upstreamType == "DATA_LOSS"):
		status = upstreamType
	}

	result := &APIError{
		Code:    code,
		Status:  status,
		Message: err.Error(),
	}
	if delay := provider.RetryAfterFromError(err); delay > 0 {
		seconds := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%09d", delay/time.Second, delay%time.Second), "0"), ".")
		result.Details = []map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": seconds + "s"}}
	}
	return result
}

// writeSSERetry emits an SSE "retry:" field (milliseconds) if the error
// carries a RetryAfter duration. Must be written before the data line of
// the SSE message block it belongs to.
func writeSSERetry(w http.ResponseWriter, err error) {
	d := provider.RetryAfterFromError(err)
	if d <= 0 {
		return
	}

	ms := max(d.Milliseconds(), 1)

	fmt.Fprintf(w, "retry: %d\n", ms)
}
