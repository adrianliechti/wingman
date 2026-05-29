package responses

import (
	"bytes"
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const customToolModel = "custom-tool-model"

// toolCallStreamCompleter streams a single tool call as the upstream OpenAI
// Responses SDK would: an output_item.added (name only, empty args) followed
// by chunks of arguments — mirroring what responder.go produces.
type toolCallStreamCompleter struct {
	callID    string
	callName  string
	argChunks []string
}

func (c toolCallStreamCompleter) Complete(_ context.Context, _ []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if !yield(&provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(provider.ToolCall{ID: c.callID, Name: c.callName}),
				},
			},
		}, nil) {
			return
		}

		for _, chunk := range c.argChunks {
			if !yield(&provider.Completion{
				Message: &provider.Message{
					Role: provider.MessageRoleAssistant,
					Content: []provider.Content{
						provider.ToolCallContent(provider.ToolCall{ID: c.callID, Name: c.callName, Arguments: chunk}),
					},
				},
			}, nil) {
				return
			}
		}

		yield(&provider.Completion{
			Status: provider.CompletionStatusCompleted,
		}, nil)
	}
}

// TestCustomToolCallStreamsInputDeltas verifies that a non-apply_patch custom
// tool streams response.custom_tool_call_input.delta/done events between
// output_item.added and output_item.done, and emits NO function_call_arguments
// events.
func TestCustomToolCallStreamsInputDeltas(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(customToolModel, toolCallStreamCompleter{
		callID:    "call_grammar_1",
		callName:  "lark_query",
		argChunks: []string{`SELECT * `, `FROM `, `users`},
	})

	body := []byte(`{
		"model": "` + customToolModel + `",
		"stream": true,
		"tools": [{"type":"custom","name":"lark_query","format":{"type":"grammar","syntax":"lark","definition":"start: query"}}],
		"input": "query the db"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	stream := rec.Body.String()

	mustContain := []string{
		`event: response.output_item.added`,
		`"type":"custom_tool_call"`,
		`"id":"ctc_call_grammar_1"`,
		`"name":"lark_query"`,
		`event: response.custom_tool_call_input.delta`,
		`"delta":"SELECT * "`,
		`"delta":"FROM "`,
		`"delta":"users"`,
		`event: response.custom_tool_call_input.done`,
		`"input":"SELECT * FROM users"`,
		`event: response.output_item.done`,
		`"status":"completed"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(stream, want) {
			t.Fatalf("missing %q in stream\n--- STREAM ---\n%s", want, stream)
		}
	}

	mustNotContain := []string{
		`response.function_call_arguments.delta`,
		`response.function_call_arguments.done`,
	}
	for _, bad := range mustNotContain {
		if strings.Contains(stream, bad) {
			t.Fatalf("unexpected %q in stream\n--- STREAM ---\n%s", bad, stream)
		}
	}
}

// TestApplyPatchAsCustomToolSuppressesArgDeltas verifies that when codex
// registers apply_patch as a custom tool, the JSON arg fragments from the
// upstream are NOT leaked as deltas (since the wire format converts to a
// patch envelope only at the done event).
func TestApplyPatchAsCustomToolSuppressesArgDeltas(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(customToolModel, toolCallStreamCompleter{
		callID:    "call_patch_1",
		callName:  "apply_patch",
		argChunks: []string{`{"type":"update_file","path":"main.go","diff":"@@\n-old\n+new\n"}`},
	})

	body := []byte(`{
		"model": "` + customToolModel + `",
		"stream": true,
		"tools": [{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: x"}}],
		"input": "patch please"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	stream := rec.Body.String()

	// Should NOT emit JSON-arg deltas (they wouldn't sum to the envelope input)
	if strings.Contains(stream, `event: response.custom_tool_call_input.delta`) {
		t.Fatalf("expected no input deltas for apply_patch (envelope is atomic), got\n--- STREAM ---\n%s", stream)
	}

	// Should emit the done event with the envelope as input
	if !strings.Contains(stream, `event: response.custom_tool_call_input.done`) {
		t.Fatalf("expected custom_tool_call_input.done event\n--- STREAM ---\n%s", stream)
	}
	if !strings.Contains(stream, `*** Begin Patch`) || !strings.Contains(stream, `*** Update File: main.go`) || !strings.Contains(stream, `*** End Patch`) {
		t.Fatalf("expected patch envelope in done event\n--- STREAM ---\n%s", stream)
	}
}

// TestApplyPatchNativeStreamsAddedAndDone verifies that when the request uses
// the native apply_patch tool type, the stream contains output_item.added with
// type=apply_patch_call followed by output_item.done with operation populated,
// and emits NO function_call_arguments delta/done events.
func TestApplyPatchNativeStreamsAddedAndDone(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(customToolModel, toolCallStreamCompleter{
		callID:    "call_native_1",
		callName:  "apply_patch",
		argChunks: []string{`{"type":"update_file","path":"x.go","diff":"@@\n-a\n+b\n"}`},
	})

	body := []byte(`{
		"model": "` + customToolModel + `",
		"stream": true,
		"tools": [{"type":"apply_patch"}],
		"input": "patch please"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	stream := rec.Body.String()

	mustContain := []string{
		`event: response.output_item.added`,
		`"type":"apply_patch_call"`,
		`"id":"apc_call_native_1"`,
		`event: response.output_item.done`,
		`"operation":{"type":"update_file","path":"x.go","diff":"@@\n-a\n+b\n"}`,
	}
	for _, want := range mustContain {
		if !strings.Contains(stream, want) {
			t.Fatalf("missing %q in stream\n--- STREAM ---\n%s", want, stream)
		}
	}

	mustNotContain := []string{
		`response.function_call_arguments.delta`,
		`response.function_call_arguments.done`,
		`response.custom_tool_call_input.delta`,
		`response.custom_tool_call_input.done`,
	}
	for _, bad := range mustNotContain {
		if strings.Contains(stream, bad) {
			t.Fatalf("unexpected %q in stream\n--- STREAM ---\n%s", bad, stream)
		}
	}
}

// TestFunctionToolStillUsesFunctionEvents guards against regressing the
// function tool wire path while adding custom_tool_call_input streaming.
func TestFunctionToolStillUsesFunctionEvents(t *testing.T) {
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(customToolModel, toolCallStreamCompleter{
		callID:    "call_fn_1",
		callName:  "get_weather",
		argChunks: []string{`{"city":`, `"Zurich"}`},
	})

	body := []byte(`{
		"model": "` + customToolModel + `",
		"stream": true,
		"tools": [{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}],
		"input": "weather?"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	stream := rec.Body.String()

	mustContain := []string{
		`"type":"function_call"`,
		`"id":"fc_call_fn_1"`,
		`event: response.function_call_arguments.delta`,
		`"delta":"{\"city\":"`,
		`"delta":"\"Zurich\"}"`,
		`event: response.function_call_arguments.done`,
		`"arguments":"{\"city\":\"Zurich\"}"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(stream, want) {
			t.Fatalf("missing %q in stream\n--- STREAM ---\n%s", want, stream)
		}
	}

	if strings.Contains(stream, `response.custom_tool_call_input.delta`) {
		t.Fatalf("function tool should not emit custom_tool_call_input events\n--- STREAM ---\n%s", stream)
	}
}
