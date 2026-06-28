package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/openai/openai-go/v3"
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

func convertedMessages(t *testing.T, messages []provider.Message) []map[string]any {
	t.Helper()

	completer, err := NewCompleter("http://localhost", "test")
	if err != nil {
		t.Fatalf("new completer: %v", err)
	}

	converted, err := completer.convertMessages(messages)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}

	data, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	return result
}

// TestConvertMessages_ToolResultWithText verifies a user message carrying both
// tool results and text (canonical Anthropic shape) emits the tool messages
// and keeps the text in a trailing user message instead of dropping it.
func TestConvertMessages_ToolResultWithText(t *testing.T) {
	result := convertedMessages(t, []provider.Message{
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "42"}}}),
				provider.TextContent("now explain the result"),
			},
		},
	})

	if len(result) != 2 {
		t.Fatalf("expected tool + user message, got %d: %v", len(result), result)
	}

	if result[0]["role"] != "tool" || result[0]["tool_call_id"] != "call_1" || result[0]["content"] != "42" {
		t.Errorf("tool message: %v", result[0])
	}
	if result[1]["role"] != "user" {
		t.Errorf("expected trailing user message, got %v", result[1])
	}
}

// TestConvertMessages_ToolResultImageBridged verifies image parts in tool
// results (e.g. screenshots) are bridged into a user message since OpenAI tool
// messages are text-only.
func TestConvertMessages_ToolResultImageBridged(t *testing.T) {
	img := &provider.File{ContentType: "image/png", Content: []byte{1, 2, 3}}

	result := convertedMessages(t, []provider.Message{
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "screenshot taken"}, {File: img}}}),
			},
		},
	})

	if len(result) != 2 {
		t.Fatalf("expected tool + user message, got %d: %v", len(result), result)
	}

	if result[0]["role"] != "tool" || result[0]["content"] != "screenshot taken" {
		t.Errorf("tool message: %v", result[0])
	}

	parts, ok := result[1]["content"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("user message parts: %v", result[1]["content"])
	}
	if parts[0].(map[string]any)["type"] != "image_url" {
		t.Errorf("expected image_url part, got %v", parts[0])
	}
}

// TestToUsage_CacheInclusiveInputTokens verifies that OpenAI's prompt_tokens
// (already cache-inclusive) maps straight to InputTokens, with cached_tokens
// exposed as the cached subset.
func TestToUsage_CacheInclusiveInputTokens(t *testing.T) {
	usage := toUsage(openai.CompletionUsage{
		PromptTokens:     100,
		CompletionTokens: 7,
		TotalTokens:      107,
		PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
			CachedTokens: 40,
		},
		CompletionTokensDetails: openai.CompletionUsageCompletionTokensDetails{
			ReasoningTokens: 3,
		},
	})

	if usage == nil {
		t.Fatal("expected usage")
	}

	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (cache-inclusive prompt_tokens)", usage.InputTokens)
	}
	if usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", usage.OutputTokens)
	}
	if usage.ReasoningTokens != 3 {
		t.Errorf("ReasoningTokens = %d, want 3", usage.ReasoningTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", usage.CacheReadInputTokens)
	}

	if usage.CacheReadInputTokens > usage.InputTokens {
		t.Errorf("cache read tokens (%d) exceed InputTokens (%d)", usage.CacheReadInputTokens, usage.InputTokens)
	}
}

func TestConvertMessages_ToolResultOnly(t *testing.T) {
	result := convertedMessages(t, []provider.Message{
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1", Parts: []provider.Part{{Text: "42"}}}),
			},
		},
	})

	if len(result) != 1 {
		t.Fatalf("expected single tool message, got %d: %v", len(result), result)
	}
	if result[0]["role"] != "tool" {
		t.Errorf("got %v", result[0])
	}
}
