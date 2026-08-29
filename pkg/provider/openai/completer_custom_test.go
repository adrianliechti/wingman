package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/tools/custom"
)

// Chat Completions has no freeform tool type, so a custom tool is emulated as
// a function tool with a single string parameter rather than falling through
// to the generic JSON path with no schema.
func TestConvertTools_CustomEmulated(t *testing.T) {
	tools, err := convertTools([]provider.Tool{
		{Kind: provider.ToolKindCustom, Name: "run_python", Description: "Run a script."},
	})

	if err != nil {
		t.Fatalf("a custom tool must be emulated, not rejected: %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	data, err := json.Marshal(tools[0])

	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}

	var decoded struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	}

	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal tool: %v", err)
	}

	if decoded.Type != "function" || decoded.Function.Name != "run_python" {
		t.Fatalf("expected a function tool named run_python, got %s", data)
	}

	props, _ := decoded.Function.Parameters["properties"].(map[string]any)

	if len(props) != 1 {
		t.Fatalf("expected exactly one parameter, got %v", props)
	}

	if _, ok := props[custom.InputParameter]; !ok {
		t.Fatalf("expected the %q parameter, got %v", custom.InputParameter, props)
	}
}

// An upstream that repeats the function name on later fragments must not reset
// the freeform buffer: re-creating it mid-stream would silently discard every
// fragment accumulated so far, and the client would receive a custom tool call
// with a truncated (or empty) input.
func TestCompleterCustomToolBufferSurvivesRepeatedName(t *testing.T) {
	chunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"run_python","arguments":"{\"input\":\"print("}}]}}]}`,
		// same call, name repeated — the reset bug dropped everything above here
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"run_python","arguments":"\\\"a — b\\\""}}]}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":")\"}"}}]}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n"))
		}

		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "test")

	if err != nil {
		t.Fatalf("new completer: %v", err)
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindCustom, Name: "run_python"}},
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, options) {
		if err != nil {
			t.Fatalf("complete: %v", err)
		}

		acc.Add(*completion)
	}

	calls := acc.Result().Message.ToolCalls()

	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %+v", len(calls), calls)
	}

	const want = `print("a — b")`

	if calls[0].Arguments != want {
		t.Fatalf("freeform input lost fragments:\n want %q\n got  %q", want, calls[0].Arguments)
	}
}

// A stream cut short by the token limit leaves the emulated wrapper unfinished.
// The flush must recover the freeform text that arrived — handing the raw
// `{"input":"...` prefix to a client that registered the tool as freeform would
// surface JSON where it expects source code.
func TestCompleterCustomToolFlushDoesNotLeakWrapperOnTruncation(t *testing.T) {
	chunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_t","function":{"name":"run_python","arguments":"{\"input\":\"print("}}]}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n"))
		}

		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "test")

	if err != nil {
		t.Fatalf("new completer: %v", err)
	}

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{{Kind: provider.ToolKindCustom, Name: "run_python"}},
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, options) {
		if err != nil {
			t.Fatalf("complete: %v", err)
		}

		acc.Add(*completion)
	}

	calls := acc.Result().Message.ToolCalls()

	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %+v", len(calls), calls)
	}

	if strings.Contains(calls[0].Arguments, `"`+custom.InputParameter+`"`) {
		t.Fatalf("the emulated wrapper leaked to the client: %q", calls[0].Arguments)
	}

	if calls[0].Arguments != "print(" {
		t.Fatalf("expected the partial freeform text:\n want %q\n got  %q", "print(", calls[0].Arguments)
	}
}

// finish_reason drives whether downstream marks the turn truncated, which is
// what makes a partial tool call distinguishable from a malformed one.
func TestToCompletionStatusMapsFinishReason(t *testing.T) {
	tests := []struct {
		reason string
		want   provider.CompletionStatus
	}{
		{"length", provider.CompletionStatusIncomplete},
		{"content_filter", provider.CompletionStatusRefused},
		{"stop", ""},
		{"tool_calls", ""},
		{"", ""},
	}

	for _, tt := range tests {
		if got := toCompletionStatus(tt.reason); got != tt.want {
			t.Errorf("toCompletionStatus(%q) = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

// End-to-end over the stream: a turn cut short by the token limit must surface
// as an incomplete completion, not a normal one.
func TestCompleterReportsIncompleteOnLengthFinishReason(t *testing.T) {
	chunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n"))
		}

		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "test")

	if err != nil {
		t.Fatalf("new completer: %v", err)
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range completer.Complete(t.Context(), []provider.Message{provider.UserMessage("hi")}, nil) {
		if err != nil {
			t.Fatalf("complete: %v", err)
		}

		acc.Add(*completion)
	}

	if status := acc.Result().Status; status != provider.CompletionStatusIncomplete {
		t.Fatalf("status = %q, want %q", status, provider.CompletionStatusIncomplete)
	}
}
