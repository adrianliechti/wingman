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
