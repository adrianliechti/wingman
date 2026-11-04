package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"

	claude "github.com/anthropics/anthropic-sdk-go/v2"
)

func TestResponseEncodingFailure(t *testing.T) {
	// Invalid upstream tool input must not produce an empty successful response
	// or an SSE event with an empty data field.
	message := Message{Content: []ContentBlock{{Type: "tool_use", Input: jsontext.Value(`{"broken":`)}}}
	t.Run("JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeJson(rec, message)
		var response ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusInternalServerError || response.Type != "error" || response.Error.Type != "api_error" {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
		}
	})
	t.Run("SSE", func(t *testing.T) {
		rec := httptest.NewRecorder()
		if err := writeEvent(rec, "message_start", MessageStartEvent{Type: "message_start", Message: message}); err == nil {
			t.Fatal("expected encoding error")
		}
		if rec.Body.Len() != 0 || rec.Flushed {
			t.Fatalf("invalid event reached client: %s", rec.Body)
		}
	})
}

func TestMessagesPreserveErrorDetails(t *testing.T) {
	apierr := &claude.Error{RequestID: "req_upstream_trace"}
	if err := apierr.UnmarshalJSON([]byte(`{"type":"error","error":{"type":"conflict_error","message":"Request conflicts","details":{"error_code":"future_conflict","resource_ids":["one","two"]}}}`)); err != nil {
		t.Fatal(err)
	}
	upstream := &provider.ProviderError{Code: http.StatusConflict, Message: apierr.Message, Err: fmt.Errorf("wrapped: %w", apierr)}
	for _, stream := range []bool{false, true} {
		for _, started := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/started=%t", stream, started), func(t *testing.T) {
				var chunks []provider.Completion
				if started {
					chunks = append(chunks, messagesContractChunk(provider.TextContent("Partial")))
				}
				rec := messagesContractRequest(t, chunks, upstream, stream)
				body := rec.Body.Bytes()
				if stream && started {
					events, err := harness.ParseSSE(rec.Body)
					if err != nil {
						t.Fatal(err)
					}
					if len(events) == 0 || events[len(events)-1].Event != "error" {
						t.Fatalf("expected terminal error: %s", body)
					}
					body = []byte(events[len(events)-1].Raw)
				} else if rec.Code != http.StatusConflict {
					t.Fatalf("HTTP %d: %s", rec.Code, body)
				}
				var decoded claude.Error
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Message != apierr.Message || decoded.Type() != apierr.Type() || decoded.ErrorCode() != apierr.ErrorCode() || decoded.Details.RawJSON() != apierr.Details.RawJSON() {
					t.Fatalf("lost structured error information: %s", body)
				}
				var envelope ErrorResponse
				if err := json.Unmarshal(body, &envelope); err != nil || envelope.RequestID != apierr.RequestID {
					t.Fatalf("lost upstream request ID: %s (%v)", body, err)
				}
				if !(stream && started) && rec.Header().Get("Request-Id") != apierr.RequestID {
					t.Fatal("lost upstream request ID header")
				}
			})
		}
	}
}

func TestMessagesPreserveExecutionUsage(t *testing.T) {
	chunks := []provider.Completion{
		{Usage: &provider.Usage{InputTokens: 18, OutputTokens: 7, ServiceTier: "standard", InferenceGeo: "global", CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{}}},
		messagesContractChunk(provider.TextContent("hello")),
		{Usage: &provider.Usage{OutputTokens: 10}},
	}
	rec := messagesContractRequest(t, chunks, nil, false)
	var message Message
	if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if u := message.Usage; u.InputTokens != 18 || u.OutputTokens != 10 || u.ServiceTier != "standard" || u.InferenceGeo != "global" {
		t.Fatalf("JSON usage lost metadata: %+v", u)
	}
	rec = messagesContractRequest(t, chunks, nil, true)
	events, err := harness.ParseSSE(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Event != "message_start" {
		t.Fatal("missing message_start")
	}
	var start MessageStartEvent
	if err := json.Unmarshal([]byte(events[0].Raw), &start); err != nil {
		t.Fatal(err)
	}
	if u := start.Message.Usage; u.InputTokens != 18 || u.OutputTokens != 7 || u.ServiceTier != "standard" || u.InferenceGeo != "global" {
		t.Fatalf("message_start usage differs from upstream: %+v", u)
	}
	if start.Message.Usage.CacheCreation == nil {
		t.Fatal("message_start lost the cache breakdown")
	}
	delta := events[len(events)-2]
	if delta.Event != "message_delta" {
		t.Fatal("missing terminal message_delta")
	}
	usage := delta.Data["usage"].(map[string]any)
	for _, field := range []string{"cache_read_input_tokens", "cache_creation_input_tokens"} {
		if value, present := usage[field]; !present || value != float64(0) {
			t.Fatalf("message_delta must include zero-valued %s: %s", field, delta.Raw)
		}
	}
	if _, repeated := usage["cache_creation"]; repeated {
		t.Fatalf("message_delta repeated unchanged cache breakdown: %s", delta.Raw)
	}
}
