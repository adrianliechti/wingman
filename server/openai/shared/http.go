package shared

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func WriteJson(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
}

func WriteError(w http.ResponseWriter, code int, err error) {
	// Use real status code from upstream provider if available
	code = provider.StatusCodeFromError(err, code)

	// Propagate Retry-After from upstream
	if v := provider.RetryAfterHeaderValue(provider.RetryAfterFromError(err)); v != "" {
		w.Header().Set("Retry-After", v)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	errorType := "invalid_request"

	switch {
	case code == http.StatusUnauthorized:
		errorType = "authentication_error"
	case code == http.StatusForbidden:
		errorType = "permission_error"
	case code == http.StatusNotFound:
		errorType = "not_found"
	case code == http.StatusTooManyRequests:
		errorType = "rate_limit_exceeded"
	case code >= 500:
		errorType = "server_error"
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
