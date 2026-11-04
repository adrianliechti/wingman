package responses

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
)

const errorTestModel = "error-test-model"

// midStreamErrCompleter emits one text chunk then returns an error.
type midStreamErrCompleter struct {
	err error
}

func TestResponseFailedIncludesRetryHeaders(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(errorTestModel, midStreamErrCompleter{err: &provider.ProviderError{
		Code: 429, Message: "Try again in 1s", RetryAfter: 2250 * time.Millisecond,
	}})
	rec := httptest.NewRecorder()
	New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"`+errorTestModel+`","stream":true,"input":"x"}`)))
	if !strings.Contains(rec.Body.String(), "retry: 2250\n") {
		t.Fatal("SSE retry advice lost")
	}
	events, err := harness.ParseSSE(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event != "response.failed" {
			continue
		}
		response := event.Data["response"].(map[string]any)
		headers := response["error"].(map[string]any)["headers"].(map[string]any)
		if headers["Retry-After"] != "3" {
			t.Fatalf("retry header = %v, want ceiling of 2.25s", headers)
		}
		return
	}
	t.Fatal("no response.failed event")
}

func TestResponseFailedErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		status int
		want   string
	}{
		{"invalid_image", 400, "invalid_image"},
		{"server_is_overloaded", 503, "server_error"},
		{"overloaded_error", 529, "server_error"},
		{"insufficient_quota", 429, "insufficient_quota"},
		{"credit_balance_exhausted", 429, "insufficient_quota"},
		{"throttlingException", 429, "rate_limit_exceeded"},
		{"context_length_exceeded", 400, "context_length_exceeded"},
		{"UNAVAILABLE", 503, "server_error"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterCompleter(errorTestModel, midStreamErrCompleter{err: &provider.ProviderError{Code: tc.status, Type: tc.typ, Message: "failed"}})
			rec := httptest.NewRecorder()
			New(cfg).handleResponses(rec, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"`+errorTestModel+`","stream":true,"input":"x"}`)))
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			seenError, seenFailed := false, false
			for _, event := range events {
				switch event.Event {
				case "error":
					seenError = true
					if event.Data["code"] != tc.typ {
						t.Fatalf("provider code lost: %v", event.Data)
					}
				case "response.failed":
					seenFailed = true
					failure := event.Data["response"].(map[string]any)["error"].(map[string]any)
					if failure["code"] != tc.want {
						t.Fatalf("invalid response error code: %v", failure)
					}
				}
			}
			if !seenError || !seenFailed {
				t.Fatal("missing error events")
			}
		})
	}
}

func (c midStreamErrCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.TextContent("hi ")},
			},
		}, nil) {
			return
		}

		yield(nil, c.err)
	}
}

func TestResponseErrorPrecedesResponseFailed(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(errorTestModel, midStreamErrCompleter{
		err: errors.New("upstream blew up"),
	})

	body := []byte(`{
		"model": "` + errorTestModel + `",
		"stream": true,
		"input": "x"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	stream := rec.Body.String()

	errorIdx := strings.Index(stream, "event: error")
	failedIdx := strings.Index(stream, "event: response.failed")

	if errorIdx < 0 {
		t.Fatalf("expected error event\n--- STREAM ---\n%s", stream)
	}
	if failedIdx < 0 {
		t.Fatalf("expected response.failed event\n--- STREAM ---\n%s", stream)
	}
	if errorIdx >= failedIdx {
		t.Fatalf("expected error BEFORE response.failed; got error at %d, failed at %d\n--- STREAM ---\n%s", errorIdx, failedIdx, stream)
	}

	mustContain := []string{
		`"type":"error"`,
		`"param":null`,
		`"message":"upstream blew up"`,
		`"type":"response.failed"`,
		`"status":"failed"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(stream, want) {
			t.Fatalf("missing %q in stream\n--- STREAM ---\n%s", want, stream)
		}
	}
}

func TestResponseErrorOnUpfrontFailureReturnsHTTPError(t *testing.T) {
	// When the upstream errors before any SSE headers are sent, the handler
	// writes a JSON HTTP error and does not emit SSE error events.
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(errorTestModel, upfrontErrCompleter{err: errors.New("nope")})

	body := []byte(`{
		"model": "` + errorTestModel + `",
		"stream": true,
		"input": "x"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("did not expect SSE event payload in JSON error response: %s", rec.Body.String())
	}
}

type upfrontErrCompleter struct {
	err error
}

func (c upfrontErrCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(nil, c.err)
	}
}

func TestAccumulatorErrorEmitsBothEvents(t *testing.T) {
	var events []StreamEventType
	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		events = append(events, e.Type)
		return nil
	})

	if err := acc.Error(errors.New("boom")); err != nil {
		t.Fatalf("Error: %v", err)
	}

	// Should have: response.created, response.in_progress, response.error, response.failed
	want := []StreamEventType{
		StreamEventResponseCreated,
		StreamEventResponseInProgress,
		StreamEventResponseError,
		StreamEventResponseFailed,
	}
	if len(events) != len(want) {
		t.Fatalf("event count mismatch: got %v, want %v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event %d: got %s, want %s", i, events[i], w)
		}
	}
}
