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

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const errorTestModel = "error-test-model"

// midStreamErrCompleter emits one text chunk then returns an error.
type midStreamErrCompleter struct {
	err error
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

	errorIdx := strings.Index(stream, "event: response.error")
	failedIdx := strings.Index(stream, "event: response.failed")

	if errorIdx < 0 {
		t.Fatalf("expected response.error event\n--- STREAM ---\n%s", stream)
	}
	if failedIdx < 0 {
		t.Fatalf("expected response.failed event\n--- STREAM ---\n%s", stream)
	}
	if errorIdx >= failedIdx {
		t.Fatalf("expected response.error BEFORE response.failed; got error at %d, failed at %d\n--- STREAM ---\n%s", errorIdx, failedIdx, stream)
	}

	mustContain := []string{
		`"type":"response.error"`,
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
	// writes a JSON HTTP error and does not emit response.error events.
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

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "response.error") {
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
