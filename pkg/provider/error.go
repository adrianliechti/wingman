package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrRejected marks a request Wingman refuses before any upstream call, such
// as an exhausted budget. Routers surface it as is: failing over cannot help,
// and it says nothing about upstream health.
var ErrRejected = errors.New("request rejected")

// ErrorKind classifies a failure independently of the vendor that reported it
// and of the transport. A streamed error has no HTTP status of its own, but
// its kind still determines the status a client receives.
type ErrorKind string

const (
	ErrorKindBilling          ErrorKind = "billing"
	ErrorKindUnprocessable    ErrorKind = "unprocessable"
	ErrorKindInvalidArgument  ErrorKind = "invalid_argument"
	ErrorKindUnauthenticated  ErrorKind = "unauthenticated"
	ErrorKindPermissionDenied ErrorKind = "permission_denied"
	ErrorKindNotFound         ErrorKind = "not_found"
	ErrorKindConflict         ErrorKind = "conflict"
	ErrorKindPayloadTooLarge  ErrorKind = "payload_too_large"

	// A rate limit clears after a wait; an exhausted quota or spend limit
	// does not. Both are reported as 429.
	ErrorKindRateLimited   ErrorKind = "rate_limited"
	ErrorKindQuotaExceeded ErrorKind = "quota_exceeded"

	// The caller went away. It says nothing about upstream health.
	ErrorKindCancelled ErrorKind = "cancelled"

	ErrorKindUnimplemented    ErrorKind = "unimplemented"
	ErrorKindUnavailable      ErrorKind = "unavailable"
	ErrorKindDeadlineExceeded ErrorKind = "deadline_exceeded"
	ErrorKindInternal         ErrorKind = "internal"
)

// Status returns the HTTP status for the kind, or 0 for an unknown kind.
// Internal failures happen behind the gateway and map to 502.
func (k ErrorKind) Status() int {
	switch k {
	case ErrorKindInvalidArgument:
		return http.StatusBadRequest
	case ErrorKindBilling:
		return http.StatusPaymentRequired
	case ErrorKindUnprocessable:
		return http.StatusUnprocessableEntity
	case ErrorKindUnauthenticated:
		return http.StatusUnauthorized
	case ErrorKindPermissionDenied:
		return http.StatusForbidden
	case ErrorKindNotFound:
		return http.StatusNotFound
	case ErrorKindConflict:
		return http.StatusConflict
	case ErrorKindPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case ErrorKindRateLimited, ErrorKindQuotaExceeded:
		return http.StatusTooManyRequests
	case ErrorKindCancelled:
		return 499
	case ErrorKindUnimplemented:
		return http.StatusNotImplemented
	case ErrorKindUnavailable:
		return http.StatusServiceUnavailable
	case ErrorKindDeadlineExceeded:
		return http.StatusGatewayTimeout
	case ErrorKindInternal:
		return http.StatusBadGateway
	}

	return 0
}

// Retryable reports whether the same request can succeed later. An exhausted
// quota or spend limit does not clear by waiting.
func (k ErrorKind) Retryable() bool {
	switch k {
	case ErrorKindRateLimited, ErrorKindUnavailable, ErrorKindDeadlineExceeded, ErrorKindInternal:
		return true
	}

	return false
}

// ErrorKindForStatus classifies an HTTP status, or returns "" below 400.
func ErrorKindForStatus(code int) ErrorKind {
	switch code {
	case http.StatusUnprocessableEntity:
		return ErrorKindUnprocessable
	case http.StatusBadRequest:
		return ErrorKindInvalidArgument
	case http.StatusUnauthorized:
		return ErrorKindUnauthenticated
	case http.StatusPaymentRequired:
		return ErrorKindBilling
	case http.StatusForbidden:
		return ErrorKindPermissionDenied
	case http.StatusNotFound:
		return ErrorKindNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorKindDeadlineExceeded
	case http.StatusConflict:
		return ErrorKindConflict
	case http.StatusRequestEntityTooLarge:
		return ErrorKindPayloadTooLarge
	case http.StatusTooManyRequests:
		return ErrorKindRateLimited
	case 499:
		return ErrorKindCancelled
	case http.StatusNotImplemented:
		return ErrorKindUnimplemented
	case http.StatusServiceUnavailable, 529:
		return ErrorKindUnavailable
	}

	switch {
	case code >= 500:
		return ErrorKindInternal
	case code >= 400:
		return ErrorKindInvalidArgument
	}

	return ""
}

// Vendor names the API vocabulary of a ProviderError's Type.
type Vendor string

const (
	VendorOpenAI    Vendor = "openai"
	VendorAnthropic Vendor = "anthropic"
	VendorGoogle    Vendor = "google"
	VendorBedrock   Vendor = "bedrock"
	VendorTypeSafe  Vendor = "typesafe"
)

// ProviderError carries the classification and native details of a failure.
type ProviderError struct {
	Kind ErrorKind

	// Type is the vendor's own error type or code, such as
	// "insufficient_quota" or "RESOURCE_EXHAUSTED".
	Type string

	// Vendor is the vocabulary of Type. Empty means Wingman's own or unknown.
	Vendor Vendor

	// RawJSON and RequestID preserve optional diagnostics for the same API.
	RawJSON   string
	RequestID string

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

// CodeFromError returns the default HTTP status for a classified failure.
func CodeFromError(err error, fallback int) int {
	if code := KindFromError(err).Status(); code > 0 {
		return code
	}

	return fallback
}

// KindFromError recognizes provider errors, cancellation and transport timeouts.
func KindFromError(err error) ErrorKind {
	if provErr, ok := errors.AsType[*ProviderError](err); ok && provErr.Kind != "" {
		return provErr.Kind
	}

	switch {
	case errors.Is(err, context.Canceled):
		return ErrorKindCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorKindDeadlineExceeded
	}

	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return ErrorKindDeadlineExceeded
	}

	return ""
}

// InvalidRequest marks an error raised while building a request, before
// anything is sent upstream, as a client error (400). Errors that already
// carry a classification are returned unchanged.
func InvalidRequest(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*ProviderError](err); ok {
		return err
	}

	return &ProviderError{
		Kind:    ErrorKindInvalidArgument,
		Type:    "invalid_request_error",
		Message: err.Error(),
		Err:     err,
	}
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

// NativeError returns the original diagnostics only to a handler for that API.
func NativeError(err error, vendor Vendor) *ProviderError {
	if e, ok := errors.AsType[*ProviderError](err); ok && e.Vendor == vendor && vendor != "" {
		return e
	}
	return nil
}

// NativeType returns the error's Type for clients of the vendor's API. A type
// from another vendor's vocabulary would mislead them, so it is omitted.
func NativeType(err error, vendor Vendor) string {
	provErr, ok := errors.AsType[*ProviderError](err)
	if !ok || provErr.Vendor != "" && provErr.Vendor != vendor {
		return ""
	}

	return provErr.Type
}

// RetryAfterFromHeaders reads millisecond hints, fractional seconds, or an HTTP date.
func RetryAfterFromHeaders(h http.Header) time.Duration {
	for _, hint := range []struct {
		name string
		unit time.Duration
	}{
		{"retry-after-ms", time.Millisecond}, {"Retry-After", time.Second},
	} {
		value := strings.TrimSpace(h.Get(hint.name))
		amount, err := strconv.ParseFloat(value, 64)
		nanos := amount * float64(hint.unit)
		if err == nil && nanos >= 1 && nanos < float64(math.MaxInt64) {
			return time.Duration(nanos)
		}
	}
	if deadline, err := http.ParseTime(strings.TrimSpace(h.Get("Retry-After"))); err == nil {
		return max(0, time.Until(deadline))
	}
	return 0
}

// RetryAfterHeaderValue formats a Retry-After duration as an HTTP header value (seconds).
func RetryAfterHeaderValue(d time.Duration) string {
	if d <= 0 {
		return ""
	}

	secs := d / time.Second
	if d%time.Second != 0 {
		secs++
	}

	return fmt.Sprintf("%d", secs)
}
