package google

import (
	"bytes"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"google.golang.org/genai"
)

func TestStripToolIDSignature(t *testing.T) {
	signed := formatToolID("call_1", "search", []byte("SECRET_SIG"))

	tests := []struct {
		input string
		want  string
	}{
		{"call_1", "call_1"},
		{"call_1::search", "call_1::search"},
		{signed, "call_1::search"},
		{"call_1::search::", "call_1::search"},
	}

	for _, tt := range tests {
		if got := StripToolIDSignature(tt.input); got != tt.want {
			t.Errorf("StripToolIDSignature(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertContent_DummyThoughtSignature(t *testing.T) {
	message := provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{
				ID:        "call_1",
				Name:      "search",
				Arguments: `{"query":"test"}`,
			}),
		},
	}

	content, err := convertContent(message, nil)
	if err != nil {
		t.Fatalf("convertContent: %v", err)
	}

	if len(content.Parts) != 1 || content.Parts[0].FunctionCall == nil {
		t.Fatalf("expected 1 function call part, got %+v", content.Parts)
	}

	if !bytes.Equal(content.Parts[0].ThoughtSignature, dummyThoughtSignature) {
		t.Errorf("expected dummy thought signature, got %q", content.Parts[0].ThoughtSignature)
	}
}

func TestConvertContent_RealSignaturePreferred(t *testing.T) {
	message := provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{
				ID:        formatToolID("call_1", "search", []byte("REAL_SIG")),
				Name:      "search",
				Arguments: `{}`,
			}),
		},
	}

	content, err := convertContent(message, nil)
	if err != nil {
		t.Fatalf("convertContent: %v", err)
	}

	if len(content.Parts) != 1 || content.Parts[0].FunctionCall == nil {
		t.Fatalf("expected 1 function call part, got %+v", content.Parts)
	}

	if got := string(content.Parts[0].ThoughtSignature); got != "REAL_SIG" {
		t.Errorf("expected round-tripped signature REAL_SIG, got %q", got)
	}
}

func TestConvertContent_PendingSignaturePreferred(t *testing.T) {
	message := provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{
				Signature: "PENDING_SIG",
			}),
			provider.ToolCallContent(provider.ToolCall{
				ID:        "call_1",
				Name:      "search",
				Arguments: `{}`,
			}),
		},
	}

	content, err := convertContent(message, nil)
	if err != nil {
		t.Fatalf("convertContent: %v", err)
	}

	if len(content.Parts) != 1 || content.Parts[0].FunctionCall == nil {
		t.Fatalf("expected 1 function call part, got %+v", content.Parts)
	}

	if got := string(content.Parts[0].ThoughtSignature); got != "PENDING_SIG" {
		t.Errorf("expected pending signature PENDING_SIG, got %q", got)
	}
}

func TestConvertContent_ToolResultError(t *testing.T) {
	message := provider.Message{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			provider.ToolResultContent(provider.ToolResult{
				ID:      "call_1::search",
				IsError: true,
				Parts:   []provider.Part{{Text: `{"code":"permission_denied"}`}},
			}),
		},
	}

	content, err := convertContent(message, map[string]string{"call_1": "search"})
	if err != nil {
		t.Fatalf("convertContent: %v", err)
	}

	if len(content.Parts) != 1 || content.Parts[0].FunctionResponse == nil {
		t.Fatalf("expected 1 function response part, got %+v", content.Parts)
	}

	response := content.Parts[0].FunctionResponse.Response
	errorValue, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response: got %#v", response)
	}
	if errorValue["code"] != "permission_denied" {
		t.Fatalf("error code: got %v", errorValue["code"])
	}
}

// TestToCompletionUsage_ReasoningAndCacheInclusive verifies that Gemini's
// thoughts tokens are exposed as ReasoningTokens and folded into the
// reasoning-inclusive OutputTokens, and that PromptTokenCount (already
// cache-inclusive) maps to InputTokens with the cached subset preserved.
func TestToCompletionUsage_ReasoningAndCacheInclusive(t *testing.T) {
	usage := toCompletionUsage(&genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        100,
		CachedContentTokenCount: 40,
		CandidatesTokenCount:    14,
		ThoughtsTokenCount:      6,
	})

	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (cache-inclusive prompt count)", usage.InputTokens)
	}
	if usage.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d, want 20 (14 visible + 6 thinking)", usage.OutputTokens)
	}
	if usage.ReasoningTokens != 6 {
		t.Errorf("ReasoningTokens = %d, want 6", usage.ReasoningTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", usage.CacheReadInputTokens)
	}
	if usage.ReasoningTokens > usage.OutputTokens {
		t.Errorf("reasoning tokens (%d) exceed OutputTokens (%d)", usage.ReasoningTokens, usage.OutputTokens)
	}
}

func TestToContent_EmptyFunctionCallArgs(t *testing.T) {
	content := &genai.Content{
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "get_time"}},
			{FunctionCall: &genai.FunctionCall{Name: "get_weather", Args: map[string]any{"location": "Paris"}}},
		},
	}

	var calls []provider.ToolCall

	for _, c := range toContent(content, nil) {
		if c.ToolCall != nil {
			calls = append(calls, *c.ToolCall)
		}
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d: %+v", len(calls), calls)
	}
	if calls[0].Arguments != "{}" {
		t.Errorf("empty args: got %q, want {}", calls[0].Arguments)
	}
	if calls[1].Arguments != `{"location":"Paris"}` {
		t.Errorf("args: got %q", calls[1].Arguments)
	}
}
