package anthropic

import (
	"encoding/json/v2"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// errorResponse builds the same Messages error for an HTTP rejection or SSE event.
func errorResponse(err error, fallback int) (int, ErrorResponse) {
	kind := provider.KindFromError(err)
	code := kind.Status()
	switch {
	case kind == provider.ErrorKindUnavailable:
		code = 529
	case code == 0:
		code = fallback
	}
	response := ErrorResponse{Type: "error", Error: Error{Type: errorTypeForStatus(code), Message: err.Error()}}
	if upstream := provider.NativeError(err, provider.VendorAnthropic); upstream != nil {
		response.RequestID = upstream.RequestID
		var native ErrorResponse
		if upstream.RawJSON != "" && json.Unmarshal([]byte(upstream.RawJSON), &native) == nil && native.Error.Details.Kind() != 'n' {
			response.Error.Details = native.Error.Details
		}
	}
	return code, response
}

// errorTypeForStatus maps an HTTP status code to Anthropic's canonical
// error.type string (see https://platform.claude.com/docs/en/api/errors).
func errorTypeForStatus(code int) string {
	switch code {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusPaymentRequired:
		return "billing_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusConflict:
		return "conflict_error"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "timeout_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	}

	if code >= 500 {
		return "api_error"
	}

	return "invalid_request_error"
}
