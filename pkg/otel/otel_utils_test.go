package otel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestToChatMessageEmitsTypedParts(t *testing.T) {
	args := `{"city":"Zurich"}`
	msg := toChatMessage(provider.Message{
		Role: provider.MessageRoleAssistant,
		Content: []provider.Content{
			provider.TextContent("Sure, let me check the weather."),
			provider.ReasoningContent(provider.Reasoning{Text: "User wants Zurich weather"}),
			provider.ToolCallContent(provider.ToolCall{ID: "tc_1", Name: "get_weather", Arguments: args}),
		},
	})

	if msg.Role != "assistant" {
		t.Fatalf("role: got %q, want assistant", msg.Role)
	}
	if len(msg.Parts) != 3 {
		t.Fatalf("parts: got %d, want 3 (%+v)", len(msg.Parts), msg.Parts)
	}

	if msg.Parts[0].Type != "text" || msg.Parts[0].Content == "" {
		t.Fatalf("part 0: expected text, got %+v", msg.Parts[0])
	}
	if msg.Parts[1].Type != "reasoning" || msg.Parts[1].Content == "" {
		t.Fatalf("part 1: expected reasoning, got %+v", msg.Parts[1])
	}
	if msg.Parts[2].Type != "tool_call" || msg.Parts[2].Name != "get_weather" || msg.Parts[2].ID != "tc_1" {
		t.Fatalf("part 2: expected tool_call, got %+v", msg.Parts[2])
	}

	// Arguments should be parsed JSON, not a raw string
	argsMap, ok := msg.Parts[2].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("expected arguments parsed to object, got %T", msg.Parts[2].Arguments)
	}
	if argsMap["city"] != "Zurich" {
		t.Fatalf("expected arguments.city=Zurich, got %v", argsMap["city"])
	}
}

func TestToChatMessageToolResultPromotesRole(t *testing.T) {
	msg := toChatMessage(provider.Message{
		Role: provider.MessageRoleUser,
		Content: []provider.Content{
			provider.ToolResultContent(provider.ToolResult{ID: "tc_1", Parts: []provider.Part{{Text: "sunny, 22°"}}}),
		},
	})

	if msg.Role != "tool" {
		t.Fatalf("role: got %q, want tool (tool_result messages must use role=tool)", msg.Role)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].Type != "tool_call_response" {
		t.Fatalf("expected single tool_call_response part, got %+v", msg.Parts)
	}
	if msg.Parts[0].ID != "tc_1" {
		t.Fatalf("expected id=tc_1, got %q", msg.Parts[0].ID)
	}
	if msg.Parts[0].Response != "sunny, 22°" {
		t.Fatalf("expected response text passthrough, got %v", msg.Parts[0].Response)
	}
}

func TestFinishReasonMappings(t *testing.T) {
	cases := []struct {
		name string
		c    *provider.Completion
		want string
	}{
		{
			name: "completed-without-toolcall",
			c:    &provider.Completion{Status: provider.CompletionStatusCompleted, Message: &provider.Message{}},
			want: "stop",
		},
		{
			name: "completed-with-toolcall",
			c: &provider.Completion{Status: provider.CompletionStatusCompleted, Message: &provider.Message{Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{Name: "x"}),
			}}},
			want: "tool_call",
		},
		{
			name: "incomplete-maps-to-length",
			c:    &provider.Completion{Status: provider.CompletionStatusIncomplete},
			want: "length",
		},
		{
			name: "failed-maps-to-error",
			c:    &provider.Completion{Status: provider.CompletionStatusFailed},
			want: "error",
		},
		{
			name: "refused-maps-to-content-filter",
			c:    &provider.Completion{Status: provider.CompletionStatusRefused},
			want: "content_filter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := finishReason(tc.c); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Verify the marshaled JSON has the spec-required keys (role, parts, finish_reason
// for output) and no stray legacy "content" field at the message level.
func TestCompletionAttrsMatchesSemconvShape(t *testing.T) {
	prevDebug := EnableDebug
	EnableDebug = true
	defer func() { EnableDebug = prevDebug }()

	attrs := CompletionAttrs(&provider.Completion{
		Status: provider.CompletionStatusCompleted,
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.TextContent("hi")},
		},
	})

	if len(attrs) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(attrs))
	}
	raw := attrs[0].Value.AsString()

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("attr value must be JSON array of messages: %v (%s)", err, raw)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected one output message, got %d (%s)", len(parsed), raw)
	}
	got := parsed[0]
	for _, key := range []string{"role", "parts", "finish_reason"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("output message missing required %q field: %s", key, raw)
		}
	}
	// Flat string content at the message level was the OLD shape — must be gone.
	if _, hasFlat := got["content"]; hasFlat {
		t.Fatalf("output message must not carry flat %q field (use parts[].content): %s", "content", raw)
	}
	if !strings.Contains(raw, `"type":"text"`) {
		t.Fatalf("expected at least one text part: %s", raw)
	}
}
