package provider

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ProviderError wraps an upstream API error with HTTP status code and rate limit info.
type ProviderError struct {
	StatusCode int
	Message    string
	Err        error

	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// StatusCodeFromError extracts the HTTP status code from a ProviderError.
// Returns the fallback if the error is not a ProviderError.
func StatusCodeFromError(err error, fallback int) int {
	var provErr *ProviderError
	if errors.As(err, &provErr) && provErr.StatusCode > 0 {
		if provErr.StatusCode >= 500 {
			return http.StatusBadGateway
		}

		return provErr.StatusCode
	}

	return fallback
}

// RetryAfterFromError extracts the Retry-After duration from a ProviderError.
func RetryAfterFromError(err error) time.Duration {
	if provErr, ok := errors.AsType[*ProviderError](err); ok {
		return provErr.RetryAfter
	}

	return 0
}

// RetryAfterHeaderValue formats a Retry-After duration as an HTTP header value (seconds).
func RetryAfterHeaderValue(d time.Duration) string {
	if d <= 0 {
		return ""
	}

	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}

	return fmt.Sprintf("%d", secs)
}
