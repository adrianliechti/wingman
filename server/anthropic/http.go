package anthropic

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"

	claude "github.com/anthropics/anthropic-sdk-go/v2"
)

// Preserve structured Anthropic details through provider wrappers, including
// fields introduced after the SDK version used by this gateway.
func responseError(code int, err error) Error {
	result := Error{
		Type:    errorTypeForStatus(code),
		Message: err.Error(),
	}
	if apierr, ok := errors.AsType[*claude.Error](err); ok {
		if details := apierr.Details.RawJSON(); details != "" && details != "null" {
			result.Details = jsontext.Value(details)
		}
	}
	return result
}

func errorRequestID(err error) string {
	if apierr, ok := errors.AsType[*claude.Error](err); ok {
		return apierr.RequestID
	}
	return ""
}

// errorTypeForStatus maps an HTTP status code to Anthropic's canonical
// error.type string (see https://platform.claude.com/docs/en/api/errors).
//
// Code 0 (no upstream status) is treated as a client-side validation error.
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
