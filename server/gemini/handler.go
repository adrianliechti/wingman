package gemini

import (
	"encoding/json/v2"
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
	kind := provider.KindFromError(err)
	if kind == "" {
		kind = provider.ErrorKindForStatus(fallback)
	}
	code := kind.Status()
	var status string
	switch kind {
	case provider.ErrorKindInvalidArgument, provider.ErrorKindUnprocessable:
		code, status = http.StatusBadRequest, "INVALID_ARGUMENT"
	case provider.ErrorKindPayloadTooLarge:
		status = "INVALID_ARGUMENT"
	case provider.ErrorKindUnauthenticated:
		status = "UNAUTHENTICATED"
	case provider.ErrorKindPermissionDenied:
		status = "PERMISSION_DENIED"
	case provider.ErrorKindNotFound:
		status = "NOT_FOUND"
	case provider.ErrorKindConflict:
		status = "ABORTED"
	case provider.ErrorKindBilling, provider.ErrorKindQuotaExceeded, provider.ErrorKindRateLimited:
		code, status = http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"
	case provider.ErrorKindCancelled:
		status = "CANCELLED"
	case provider.ErrorKindUnimplemented:
		status = "UNIMPLEMENTED"
	case provider.ErrorKindUnavailable:
		status = "UNAVAILABLE"
	case provider.ErrorKindDeadlineExceeded:
		status = "DEADLINE_EXCEEDED"
	default:
		code, status = http.StatusInternalServerError, "INTERNAL"
	}

	// Several Google canonical statuses share an HTTP code. Preserve these
	// distinctions when supplied by a Google provider, never foreign type names.
	switch upstreamType := provider.NativeType(err, provider.VendorGoogle); {
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
