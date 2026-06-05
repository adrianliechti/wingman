package google

import (
	"bytes"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
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
