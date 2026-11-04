package google

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
	"google.golang.org/genai/interactions/models/components"
	"google.golang.org/genai/interactions/models/interactions"
)

func TestConvertErrorMetadata(t *testing.T) {
	details := []map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "2.25s"}}
	for _, tc := range []struct {
		name   string
		err    error
		status int
		retry  time.Duration
	}{
		{"genai value", genai.APIError{Code: 429, Status: "RESOURCE_EXHAUSTED", Message: "busy", Details: details}, 429, 2250 * time.Millisecond},
		{"genai pointer", &genai.APIError{Code: 429, Status: "RESOURCE_EXHAUSTED", Message: "busy", Details: details}, 429, 2250 * time.Millisecond},
		{"typed interaction", &apierrors.CreateInteractionServerError{
			Error_:   interactions.Error{Code: new("UNAVAILABLE"), Message: new("busy")},
			HTTPMeta: components.HTTPMetadata{Response: &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {"2.25"}}}},
		}, 503, 2250 * time.Millisecond},
		{"missing HTTP metadata", &apierrors.CreateInteractionClientError{Error_: interactions.Error{Code: new("RESOURCE_EXHAUSTED"), Message: new("busy")}}, 429, 0},
		{"numeric gateway stream", &apierrors.ResponseValidationError{
			StatusCode: 200, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"busy","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"2.25s"}]}}`,
		}, 429, 2250 * time.Millisecond},
		{"HTTP status authoritative", &apierrors.APIError{StatusCode: 503, Body: `{"error":{"code":"429","message":"busy"}}`}, 503, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := fmt.Errorf("wrapped: %w", tc.err)
			converted := convertError(original)
			if provider.CodeFromError(converted, 0) != tc.status || provider.RetryAfterFromError(converted) != tc.retry || converted.Error() != "busy" || !errors.Is(converted, original) {
				t.Fatalf("converted=%#v", converted)
			}
		})
	}
	for _, delay := range []string{"-1s", "invalid", "1000000000000000000s"} {
		if got := retryFromDetails([]map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": delay}}); got != 0 {
			t.Fatalf("invalid retry delay %q: %v", delay, got)
		}
	}
}

func TestCompleteCanonicalStreamErrors(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"RESOURCE_EXHAUSTED", 429}, {"UNAVAILABLE", 503},
		{"DEADLINE_EXCEEDED", 504}, {"INVALID_ARGUMENT", 400},
		{"429", 429}, {"https://example.test/unknown-error", 502},
	} {
		t.Run(tc.code, func(t *testing.T) {
			handler := interactionEvents(createdEvent, fmt.Sprintf(`{"event_type":"error","error":{"code":%q,"message":"busy"}}`, tc.code))
			_, err := completeAll(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2.25")
				handler(w, r)
			}, nil)
			if provider.CodeFromError(err, 0) != tc.status || provider.TypeFromError(err) != tc.code || provider.RetryAfterFromError(err) != 2250*time.Millisecond || err.Error() != "busy" {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestCompleteModelOutputErrors(t *testing.T) {
	for _, tc := range []struct{ code, status int }{{8, 429}, {14, 503}, {4, 504}, {13, 502}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			_, err := completeAll(t, interactionEvents(createdEvent, fmt.Sprintf(`{"event_type":"step.start","index":0,"step":{"type":"model_output","error":{"code":%d,"message":"busy","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"2.25s"}]}}}`, tc.code)), nil)
			if provider.CodeFromError(err, 0) != tc.status || provider.RetryAfterFromError(err) != 2250*time.Millisecond || err.Error() != "busy" {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}
