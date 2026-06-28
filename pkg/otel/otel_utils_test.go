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

// UsageAttrs must emit the GenAI semconv usage keys with the spec's inclusive
// convention: gen_ai.usage.input_tokens counts every prompt token (cache_read +
// cache_creation are subsets of it) and gen_ai.usage.output_tokens counts every
// generated token (reasoning.output_tokens is a subset of it). Values below are
// the spec's own example numbers (docs/gen-ai/anthropic.md).
func TestUsageAttrsMatchesGenAISemconv(t *testing.T) {
	attrs := UsageAttrs(&provider.Usage{
		InputTokens:              100,
		OutputTokens:             180,
		ReasoningTokens:          50,
		CacheReadInputTokens:     50,
		CacheCreationInputTokens: 25,
	})

	got := map[string]int64{}
	for _, kv := range attrs {
		got[string(kv.Key)] = kv.Value.AsInt64()
	}

	want := map[string]int64{
		"gen_ai.usage.input_tokens":                100,
		"gen_ai.usage.output_tokens":               180,
		"gen_ai.usage.reasoning.output_tokens":     50,
		"gen_ai.usage.cache_read.input_tokens":     50,
		"gen_ai.usage.cache_creation.input_tokens": 25,
	}
	for key, val := range want {
		if got[key] != val {
			t.Errorf("attr %q: got %d, want %d (all attrs: %v)", key, got[key], val, got)
		}
	}

	// Spec convention: cache and reasoning are subsets of the inclusive totals.
	if got["gen_ai.usage.cache_read.input_tokens"]+got["gen_ai.usage.cache_creation.input_tokens"] > got["gen_ai.usage.input_tokens"] {
		t.Errorf("cache tokens exceed input_tokens, violating the cache-inclusive convention: %v", got)
	}
	if got["gen_ai.usage.reasoning.output_tokens"] > got["gen_ai.usage.output_tokens"] {
		t.Errorf("reasoning tokens exceed output_tokens, violating the reasoning-inclusive convention: %v", got)
	}
}

// Zero-valued usage fields are omitted, and nil usage yields no attributes.
func TestUsageAttrsOmitsZeroAndNil(t *testing.T) {
	if attrs := UsageAttrs(nil); attrs != nil {
		t.Fatalf("nil usage: expected no attrs, got %v", attrs)
	}

	attrs := UsageAttrs(&provider.Usage{InputTokens: 10, OutputTokens: 5})
	for _, kv := range attrs {
		switch string(kv.Key) {
		case "gen_ai.usage.reasoning.output_tokens",
			"gen_ai.usage.cache_read.input_tokens",
			"gen_ai.usage.cache_creation.input_tokens":
			t.Errorf("zero-valued field %q must be omitted", string(kv.Key))
		}
	}
}
