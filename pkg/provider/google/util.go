package google

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
)

func convertError(err error) error {
	var clientError *apierrors.CreateInteractionClientError
	var serverError *apierrors.CreateInteractionServerError
	var responseError *apierrors.ResponseValidationError
	var interactionError *apierrors.APIError
	switch {
	case errors.As(err, &clientError):
		return interactionProviderError(err, 0, value(clientError.Error_.Code), value(clientError.Error_.Message), "", clientError.HTTPMeta.Response)
	case errors.As(err, &serverError):
		return interactionProviderError(err, 0, value(serverError.Error_.Code), value(serverError.Error_.Message), "", serverError.HTTPMeta.Response)
	case errors.As(err, &responseError):
		return interactionProviderError(err, responseError.StatusCode, "", responseError.Message, responseError.Body, responseError.RawResponse)
	case errors.As(err, &interactionError):
		return interactionProviderError(err, interactionError.StatusCode, "", interactionError.Message, interactionError.Body, interactionError.RawResponse)
	}

	// The genai SDK returns genai.APIError as a value type.
	var apierr genai.APIError
	var apierrPtr *genai.APIError
	if errors.As(err, &apierrPtr) && apierrPtr != nil {
		apierr = *apierrPtr
	}

	if apierrPtr != nil || errors.As(err, &apierr) {
		statusCode := apierr.Code
		if statusCode < 400 {
			statusCode = statusCodeFromErrorType(apierr.Status)
		}

		// Google's API gateway returns 400/INVALID_ARGUMENT for auth errors
		// like invalid or expired API keys. Remap these to 401.
		if isAuthError(apierr) {
			statusCode = http.StatusUnauthorized
		}

		// Prefer the clean, human-readable Message field over Error(),
		// which includes the status code, details, and raw maps.
		message := apierr.Message
		if message == "" {
			message = apierr.Error()
		}

		return &provider.ProviderError{
			Code:       statusCode,
			Type:       apierr.Status,
			Message:    message,
			RetryAfter: retryFromDetails(apierr.Details),
			Err:        err,
		}
	}

	return err
}

// Interactions has distinct SDK error types. Its gateway can also return the
// original Google error envelope with a numeric code, which the generated
// Interactions schema (a string code) reports as a response validation error.
func interactionProviderError(err error, status int, code, message, body string, httpResponse *http.Response) error {
	var retryAfter time.Duration
	if httpResponse != nil {
		status = httpResponse.StatusCode
		retryAfter = parseRetryAfter(httpResponse.Header)
	}
	var response struct {
		Error struct {
			Code    jsontext.Value   `json:"code"`
			Status  string           `json:"status"`
			Message string           `json:"message"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	var details []map[string]any
	// Some non-2xx streaming failures are already framed as SSE.
	for line := range strings.SplitSeq(body, "\n") {
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			body = strings.TrimSpace(after)
			break
		}
	}
	// Streaming gateway failures can use a one-element JSON array, even
	// though the Interactions SDK expects a single error object.
	var envelopes []jsontext.Value
	if json.Unmarshal([]byte(body), &envelopes) == nil && len(envelopes) > 0 {
		body = string(envelopes[0])
	}
	if json.Unmarshal([]byte(body), &response) == nil {
		if response.Error.Message != "" {
			message = response.Error.Message
		}
		var bodyCode string
		if json.Unmarshal(response.Error.Code, &bodyCode) != nil {
			bodyCode = string(response.Error.Code)
		}
		if status < 400 {
			if numeric, e := strconv.Atoi(bodyCode); e == nil && numeric >= 400 && numeric < 600 {
				status = numeric
			}
		}
		if response.Error.Status != "" {
			code = response.Error.Status
		} else if bodyCode != "" {
			code = bodyCode
		}
		details = response.Error.Details
	}
	if status < 400 {
		status = statusCodeFromErrorType(code)
	}
	if code == "API_KEY_INVALID" || strings.Contains(message, "API key not valid") || isAuthError(genai.APIError{Details: details}) {
		status = http.StatusUnauthorized
	}
	if message == "" {
		message = err.Error()
	}
	return &provider.ProviderError{Code: status, Type: code, Message: message, RetryAfter: max(retryAfter, retryFromDetails(details)), Err: err}
}

// Interactions stream errors have no HTTP error status. Recognize canonical
// Google codes or explicit HTTP codes; unknown URI codes stay gateway errors.
func statusCodeFromErrorType(code string) int {
	if numeric, err := strconv.Atoi(code); err == nil && numeric >= 400 && numeric < 600 {
		return numeric
	}
	switch strings.ToUpper(code) {
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE":
		return http.StatusBadRequest
	case "UNAUTHENTICATED":
		return http.StatusUnauthorized
	case "PERMISSION_DENIED":
		return http.StatusForbidden
	case "NOT_FOUND":
		return http.StatusNotFound
	case "ALREADY_EXISTS", "ABORTED":
		return http.StatusConflict
	case "RESOURCE_EXHAUSTED":
		return http.StatusTooManyRequests
	case "CANCELLED":
		return 499
	case "UNIMPLEMENTED":
		return http.StatusNotImplemented
	case "UNAVAILABLE":
		return http.StatusServiceUnavailable
	case "DEADLINE_EXCEEDED":
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

func retryFromDetails(details []map[string]any) time.Duration {
	for _, detail := range details {
		if detail["@type"] != "type.googleapis.com/google.rpc.RetryInfo" {
			continue
		}
		if delay, ok := detail["retryDelay"].(string); ok {
			if d, err := time.ParseDuration(delay); err == nil && d > 0 {
				return d
			}
		}
	}
	return 0
}

func parseRetryAfter(h http.Header) time.Duration {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		nanos := seconds * float64(time.Second)
		if !math.IsNaN(nanos) && nanos > 0 && nanos < float64(math.MaxInt64) {
			return time.Duration(nanos)
		}
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return max(time.Until(deadline), 0)
	}
	return 0
}

// isAuthError detects authentication-related errors from Google's API gateway.
// These arrive as HTTP 400 but are semantically 401 (invalid credentials).
func isAuthError(apierr genai.APIError) bool {
	for _, detail := range apierr.Details {
		if reason, _ := detail["reason"].(string); reason == "API_KEY_INVALID" {
			return true
		}
	}

	return false
}
