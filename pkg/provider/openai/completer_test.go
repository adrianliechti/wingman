package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestCompleterInterleavedToolCalls(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"get_time","arguments":""}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"zone\":\"UTC\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Bern\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
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

	result := acc.Result()
	if result.Message == nil {
		t.Fatal("expected message")
	}

	calls := result.Message.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d: %+v", len(calls), calls)
	}

	if calls[0].ID != "call_a" || calls[0].Name != "get_weather" || calls[0].Arguments != `{"city":"Bern"}` {
		t.Errorf("call_a: got %+v", calls[0])
	}
	if calls[1].ID != "call_b" || calls[1].Name != "get_time" || calls[1].Arguments != `{"zone":"UTC"}` {
		t.Errorf("call_b: got %+v", calls[1])
	}
}
