package provider

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

var ErrUnsupportedContentType = errors.New("unsupported content type")

// ProviderError wraps an upstream API error with HTTP status code, a
// provider-specific error type, and rate-limit metadata.
type ProviderError struct {
	Code    int
	Type    string
	Message string
	Err     error

	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// CodeFromError extracts the HTTP status code from a ProviderError.
// Returns the fallback if the error is not a ProviderError.
//
// 5xx responses are normalized to gateway-appropriate codes:
//   - 503 (overloaded / "Slow down") is preserved as-is so clients can back off
//   - 504 (upstream timeout, e.g. Bedrock ModelTimeoutException) is preserved
//   - 529 (Anthropic overloaded) is preserved as-is for the same reason
//   - Other 5xx statuses collapse to 502 Bad Gateway
func CodeFromError(err error, fallback int) int {
	var provErr *ProviderError
	if errors.As(err, &provErr) && provErr.Code > 0 {
		switch provErr.Code {
		case http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
			return provErr.Code
		}

		if provErr.Code >= 500 {
			return http.StatusBadGateway
		}

		return provErr.Code
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

// TypeFromError extracts the provider-specific error type (e.g. "insufficient_quota",
// "rate_limit_error", "RESOURCE_EXHAUSTED") from a ProviderError. Returns "" if none
// is available.
func TypeFromError(err error) string {
	if provErr, ok := errors.AsType[*ProviderError](err); ok {
		return provErr.Type
	}

	return ""
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
