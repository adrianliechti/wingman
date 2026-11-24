package gemini

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"

	"google.golang.org/genai"
)

func TestErrorStatusAndRetryInfo(t *testing.T) {
	for _, tc := range []struct {
		code       int
		typ        string
		wantCode   int
		wantStatus string
	}{
		{503, "", 503, "UNAVAILABLE"}, {529, "overloaded_error", 503, "UNAVAILABLE"},
		{422, "", 400, "INVALID_ARGUMENT"}, {504, "", 504, "DEADLINE_EXCEEDED"}, {408, "", 504, "DEADLINE_EXCEEDED"},
		{429, "throttlingException", 429, "RESOURCE_EXHAUSTED"},
		{402, "billing_error", 429, "RESOURCE_EXHAUSTED"},
		{400, "FAILED_PRECONDITION", 400, "FAILED_PRECONDITION"},
		{409, "ALREADY_EXISTS", 409, "ALREADY_EXISTS"},
		{499, "", 499, "CANCELLED"}, {501, "", 501, "UNIMPLEMENTED"},
		{502, "", 500, "INTERNAL"}, {500, "DATA_LOSS", 500, "DATA_LOSS"},
	} {
		t.Run(fmt.Sprint(tc.code)+tc.typ, func(t *testing.T) {
			err := &provider.ProviderError{Kind: provider.ErrorKindForStatus(tc.code), Type: tc.typ, Message: "busy", RetryAfter: 2250 * time.Millisecond}
			rec := httptest.NewRecorder()
			writeError(rec, 500, err)
			var payload struct {
				Error genai.APIError `json:"error"`
			}
			if e := json.Unmarshal(rec.Body.Bytes(), &payload); e != nil {
				t.Fatal(e)
			}
			if rec.Code != tc.wantCode || payload.Error.Code != tc.wantCode || payload.Error.Status != tc.wantStatus || payload.Error.Message != "busy" || rec.Header().Get("Retry-After") != "3" {
				t.Fatalf("status=%d headers=%v error=%+v", rec.Code, rec.Header(), payload.Error)
			}
			if len(payload.Error.Details) != 1 || payload.Error.Details[0]["retryDelay"] != "2.25s" || payload.Error.Details[0]["@type"] != "type.googleapis.com/google.rpc.RetryInfo" {
				t.Fatalf("retry details=%v", payload.Error.Details)
			}
			acc := NewStreamingAccumulator("r1", "test", func(response GenerateContentResponse) error {
				if response.Error.Code != tc.wantCode || response.Error.Status != tc.wantStatus || response.Error.Details[0]["retryDelay"] != "2.25s" {
					t.Fatalf("stream error=%+v", response.Error)
				}
				return nil
			})
			if e := acc.Error(err); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type interruptedCompleter struct{}

func (interruptedCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c := assistantChunk(provider.TextContent("partial"))
		if yield(&c, nil) {
			yield(nil, &provider.ProviderError{Kind: provider.ErrorKindUnavailable, Message: "busy", RetryAfter: 1500 * time.Millisecond})
		}
	}
}

func TestStreamErrorEnvelope(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("test", interruptedCompleter{})
	req := httptest.NewRequest("POST", "/models/test:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.SetPathValue("model", "test")
	rec := httptest.NewRecorder()
	New(cfg).handleStreamGenerateContent(rec, req)
	// python-genai parses every nonempty line as JSON and identifies a server
	// failure from the first key of the error chunk.
	frames := strings.Split(strings.TrimSpace(rec.Body.String()), "\n\n")
	if rec.Code != 200 || len(frames) != 2 || !strings.Contains(frames[0], `"text":"partial"`) || !strings.HasPrefix(frames[1], `data: {"error":`) {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var envelope ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[1], "data: ")), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != 503 || envelope.Error.Status != "UNAVAILABLE" || len(envelope.Error.Details) != 1 || envelope.Error.Details[0]["retryDelay"] != "1.5s" {
		t.Fatalf("unexpected error: %+v", envelope.Error)
	}
}
