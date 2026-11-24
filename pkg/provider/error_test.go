package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestInvalidRequest(t *testing.T) {
	if InvalidRequest(nil) != nil {
		t.Fatal("nil must stay nil")
	}

	if got := CodeFromError(InvalidRequest(errors.New("bad")), http.StatusBadGateway); got != http.StatusBadRequest {
		t.Errorf("untyped error: got %d, want 400", got)
	}

	typed := &ProviderError{Kind: ErrorKindRateLimited}
	if got := InvalidRequest(typed); got != typed {
		t.Errorf("typed error must be returned unchanged, got %v", got)
	}
}

func TestRetryAfterHeaderValue(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		want  string
	}{
		{0, ""}, {-time.Second, ""}, {time.Nanosecond, "1"},
		{time.Second, "1"}, {1500 * time.Millisecond, "2"},
		{2 * time.Second, "2"}, {time.Duration(math.MaxInt64), "9223372037"},
	} {
		if got := RetryAfterHeaderValue(tc.delay); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.delay, got, tc.want)
		}
	}
}

func TestErrorKind(t *testing.T) {
	timeout := &url.Error{Op: "Post", URL: "https://upstream.test", Err: &net.DNSError{IsTimeout: true}}
	for _, tc := range []struct {
		name string
		err  error
		kind ErrorKind
		code int
	}{
		{"nil", nil, "", 0},
		{"unclassified", errors.New("boom"), "", 0},
		{"streamed kind", &ProviderError{Kind: ErrorKindInvalidArgument}, ErrorKindInvalidArgument, 400},
		{"streamed internal failure", &ProviderError{Kind: ErrorKindInternal}, ErrorKindInternal, 502},
		{"status only", &ProviderError{Kind: ErrorKindForStatus(404)}, ErrorKindNotFound, 404},
		{"billing", &ProviderError{Kind: ErrorKindForStatus(402)}, ErrorKindBilling, 402},
		{"anthropic overload", &ProviderError{Kind: ErrorKindForStatus(529)}, ErrorKindUnavailable, 503},
		{"other client error", &ProviderError{Kind: ErrorKindForStatus(418)}, ErrorKindInvalidArgument, 400},
		{"other server error", &ProviderError{Kind: ErrorKindForStatus(500)}, ErrorKindInternal, 502},
		{"wrapped", fmt.Errorf("call: %w", &ProviderError{Kind: ErrorKindQuotaExceeded}), ErrorKindQuotaExceeded, 429},
		{"caller gone", fmt.Errorf("read: %w", context.Canceled), ErrorKindCancelled, 499},
		{"deadline", context.DeadlineExceeded, ErrorKindDeadlineExceeded, 504},
		{"client timeout", timeout, ErrorKindDeadlineExceeded, 504},
		{"unclassified wrapper of a cancellation", &ProviderError{Message: "stopped", Err: context.Canceled}, ErrorKindCancelled, 499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := KindFromError(tc.err); got != tc.kind {
				t.Errorf("kind = %q, want %q", got, tc.kind)
			}
			if got := CodeFromError(tc.err, 0); got != tc.code {
				t.Errorf("code = %d, want %d", got, tc.code)
			}
		})
	}
}

func TestNativeTypeKeepsVendorVocabulary(t *testing.T) {
	for _, tc := range []struct {
		vendor Vendor
		want   string
	}{{VendorOpenAI, "invalid_function_parameters"}, {"", "invalid_function_parameters"}, {VendorAnthropic, ""}} {
		err := fmt.Errorf("wrapped: %w", &ProviderError{Type: "invalid_function_parameters", Vendor: tc.vendor})
		if got := NativeType(err, VendorOpenAI); got != tc.want {
			t.Errorf("vendor %q: got %q, want %q", tc.vendor, got, tc.want)
		}
	}
	if NativeType(errors.New("plain"), VendorOpenAI) != "" {
		t.Error("a plain error has no native type")
	}
}

func TestErrorKindRetryable(t *testing.T) {
	for kind, want := range map[ErrorKind]bool{
		ErrorKindRateLimited: true, ErrorKindUnavailable: true, ErrorKindDeadlineExceeded: true, ErrorKindInternal: true,
		ErrorKindQuotaExceeded: false, ErrorKindInvalidArgument: false, ErrorKindUnauthenticated: false, ErrorKindCancelled: false, "": false,
	} {
		if kind.Retryable() != want {
			t.Errorf("%q retryable = %t", kind, !want)
		}
	}
}
