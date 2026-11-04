package openai

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3/packages/ssestream"
)

func TestResponderFailedRetryAdvice(t *testing.T) {
	for _, tc := range []struct {
		name, code, headers, message string
		status                       int
		retry                        time.Duration
	}{
		{"header before message", "rate_limit_exceeded", `{"Retry-After":"11.5"}`, "Try again in 2s", 429, 11500 * time.Millisecond},
		{"numeric mixed case header", "rate_limit_exceeded", `{"rEtRy-AfTeR":12}`, "Try again in 2s", 429, 12 * time.Second},
		{"Azure milliseconds", "server_error", `{"retry-after-ms":"1250"}`, "unavailable", 502, 1250 * time.Millisecond},
		{"message fallback", "rate_limit_exceeded", `{}`, "Please retry after 1101 milliseconds", 429, 1101 * time.Millisecond},
		{"default fallback", "rate_limit_exceeded", `{}`, "rate limited", 429, defaultRateLimitRetry},
		{"invalid header fallback", "rate_limit_exceeded", `{"Retry-After":"invalid"}`, "Try again in 2s", 429, 2 * time.Second},
		{"invalid value fallback", "rate_limit_exceeded", `{"Retry-After":[12]}`, "Try again in 2s", 429, 2 * time.Second},
		{"invalid object fallback", "rate_limit_exceeded", `[12]`, "Try again in 2s", 429, 2 * time.Second},
		{"invalid name fallback", "rate_limit_exceeded", `{"Retry-After\n":"12"}`, "Try again in 2s", 429, 2 * time.Second},
		{"invalid field value fallback", "rate_limit_exceeded", `{"Retry-After":"12\r\n"}`, "Try again in 2s", 429, 2 * time.Second},
		{"overflow fallback", "rate_limit_exceeded", `{"Retry-After":"18446744074"}`, "Try again in 2s", 429, 2 * time.Second},
		{"server failure", "server_error", `{"Retry-After":"8"}`, "unavailable", 502, 8 * time.Second},
		{"missing code", "", `{"Retry-After":"8"}`, "unavailable", 502, 8 * time.Second},
		{"terminal classification preserved", "invalid_prompt", `{"Retry-After":"8"}`, "invalid prompt", 400, 8 * time.Second},
		{"quota classification preserved", "insufficient_quota", `{"Retry-After":"8"}`, "quota exhausted", 429, 8 * time.Second},
		{"quota has no invented retry", "credit_balance_exhausted", `{}`, "quota exhausted", 429, 0},
		{"context window is a request error", "context_length_exceeded", `{}`, "context too long", 400, 0},
		{"unknown failure", "future_server_failure", `{}`, "unknown failure", 502, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, _ := json.Marshal(tc.message)
			data := `{"type":"response.failed","response":{"error":{"code":"` + tc.code + `","message":` + string(message) + `,"headers":` + tc.headers + `}}}`
			client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + data + "\n\n")), Request: r}, nil
			})}
			responder, err := NewResponder("http://upstream.test", "test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var runErr error
			for _, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("Go")}, nil) {
				if err != nil {
					runErr = err
					break
				}
			}
			if runErr == nil {
				t.Fatal("expected provider error")
			}
			if got := provider.CodeFromError(runErr, 0); got != tc.status {
				t.Errorf("status = %d, want %d", got, tc.status)
			}
			if got := provider.RetryAfterFromError(runErr); got != tc.retry {
				t.Errorf("retry = %v, want %v", got, tc.retry)
			}
			if runErr.Error() != tc.message {
				t.Errorf("message = %q, want %q", runErr, tc.message)
			}
		})
	}
}

func TestConvertStreamErrorRetryHeaders(t *testing.T) {
	err := &ssestream.StreamError{Event: ssestream.Event{Data: []byte(`{"error":{"code":"rate_limit_exceeded","message":"Try again in 2s","headers":{"retry-after":9}}}`)}}
	got := convertError(err)
	if provider.RetryAfterFromError(got) != 9*time.Second || provider.CodeFromError(got, 0) != 429 || !errors.Is(got, err) {
		t.Fatalf("converted error = %#v", got)
	}
}

func TestResponderStandaloneErrorEvent(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		retry  time.Duration
	}{
		{"rate_limit_exceeded", 429, defaultRateLimitRetry},
		{"slow_down", 429, defaultRateLimitRetry},
		{"server_is_overloaded", 503, 0},
		{"project_spend_limit_exceeded", 429, 0},
		{"invalid_prompt", 400, 0},
		{"context_length_exceeded", 400, 0},
		{"future_error", 502, 0},
	} {
		t.Run(tc.code, func(t *testing.T) {
			client := &http.Client{Transport: responderLifecycleTransport(func(r *http.Request) (*http.Response, error) {
				data := `event: error` + "\ndata: " + `{"type":"error","sequence_number":0,"code":"` + tc.code + `","message":"Request failed","param":null}` + "\n\n"
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
			})}
			responder, err := NewResponder("http://upstream.test", "test", WithClient(client), WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			for _, err := range responder.Complete(t.Context(), []provider.Message{provider.UserMessage("Go")}, nil) {
				if err != nil {
					failure = err
					break
				}
			}
			if failure == nil || provider.CodeFromError(failure, 0) != tc.status || failure.Error() != "Request failed" || provider.RetryAfterFromError(failure) != tc.retry {
				t.Fatalf("error=%#v", failure)
			}
		})
	}
}

func TestRetryAfterHTTPDateAndInvalidDurations(t *testing.T) {
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	got := parseRetryAfter(http.Header{"Retry-After": {deadline.Format(http.TimeFormat)}})
	if got <= 59*time.Second || got > time.Minute {
		t.Errorf("HTTP-date retry = %v", got)
	}
	for _, value := range []string{"-1", "0", "NaN", "+Inf", "1e100", "18446744074"} {
		if got := parseRetryAfter(http.Header{"Retry-After": {value}, "Retry-After-Ms": {"1500"}}); got != 1500*time.Millisecond {
			t.Errorf("%q: retry = %v, want milliseconds fallback", value, got)
		}
	}
	if got := parseRetryFromMessage("Try again in 18446744074s"); got != 0 {
		t.Errorf("overflowing message retry = %v", got)
	}
}
