package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func WriteJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
}

// ErrorTypeFromError maps a ProviderError to an OpenAI-compatible error type string.
func ErrorTypeFromError(err error) string {
	code := provider.CodeFromError(err, 0)
	return errorTypeFromCode(code)
}

func errorTypeFromCode(code int) string {
	switch {
	case code == http.StatusUnauthorized:
		return "authentication_error"
	case code == http.StatusForbidden:
		return "permission_error"
	case code == http.StatusNotFound:
		return "not_found_error"
	case code == http.StatusRequestTimeout:
		return "timeout_error"
	case code == http.StatusConflict:
		return "conflict_error"
	case code == http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case code == http.StatusServiceUnavailable:
		return "overloaded_error"
	case code >= 500:
		return "server_error"
	case code >= 400:
		return "invalid_request_error"
	default:
		return "server_error"
	}
}

func WriteError(w http.ResponseWriter, code int, err error) {
	// Use real status code from upstream provider if available
	code = provider.CodeFromError(err, code)

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	errorType := errorTypeFromCode(code)

	resp := ErrorResponse{
		Error: Error{
			Type:    errorType,
			Code:    provider.TypeFromError(err),
			Message: err.Error(),
		},
	}

	var invalidValue *InvalidValueError
	if errors.As(err, &invalidValue) {
		resp.Error.Code = "invalid_value"
		resp.Error.Param = invalidValue.Param
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(resp)
}

// WriteSSERetry emits an SSE "retry:" field (milliseconds) if the error
// carries a RetryAfter duration. Must be written before the event/data lines
// of the SSE message block it belongs to.
func WriteSSERetry(w http.ResponseWriter, err error) {
	d := provider.RetryAfterFromError(err)
	if d <= 0 {
		return
	}

	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}

	fmt.Fprintf(w, "retry: %d\n", ms)
}
