package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/anthropics/anthropic-sdk-go"
)

func sseEvent(eventType, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
}

// TestCompleterStreaming verifies the Anthropic SSE stream is converted into
// provider completions: thinking, redacted thinking, text, an empty tool_use
// input normalized to "{}", and final usage.
func TestCompleterStreaming(t *testing.T) {
	events := []string{
		sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":25,"output_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG_1"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"BLOB"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":1}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"hello"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":2}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_time","input":{}}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":3}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":12}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte(e))
		}
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "claude-test")
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

	var reasonings []provider.Reasoning
	for _, c := range result.Message.Content {
		if c.Reasoning != nil {
			reasonings = append(reasonings, *c.Reasoning)
		}
	}

	if len(reasonings) != 2 {
		t.Fatalf("expected 2 reasoning entries, got %d: %+v", len(reasonings), reasonings)
	}
	if reasonings[0].Text != "let me think" || reasonings[0].Signature != "SIG_1" || reasonings[0].Redacted {
		t.Errorf("thinking: %+v", reasonings[0])
	}
	if reasonings[1].Signature != "BLOB" || !reasonings[1].Redacted {
		t.Errorf("redacted thinking: %+v", reasonings[1])
	}

	if text := result.Message.Text(); text != "hello" {
		t.Errorf("text: got %q", text)
	}

	calls := result.Message.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "toolu_1" || calls[0].Name != "get_time" || calls[0].Arguments != "{}" {
		t.Errorf("tool call: %+v", calls[0])
	}

	if result.Usage == nil || result.Usage.InputTokens != 25 || result.Usage.OutputTokens != 12 {
		t.Errorf("usage: %+v", result.Usage)
	}
}

func requestBody(t *testing.T, completer *Completer, messages []provider.Message, options *provider.CompleteOptions) map[string]any {
	t.Helper()

	req, err := completer.convertMessageRequest(messages, options)
	if err != nil {
		t.Fatalf("convertMessageRequest: %v", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	return body
}

// TestConvertRequest_ToolResultsFirst verifies tool_result blocks precede
// other content in a user message (Anthropic rejects them elsewhere).
func TestConvertRequest_ToolResultsFirst(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.TextContent("here is the result"),
				provider.ToolResultContent(provider.ToolResult{ID: "toolu_1", Parts: []provider.Part{{Text: "42"}}}),
			},
		},
	}

	body := requestBody(t, completer, messages, nil)

	msgs := body["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)

	if len(content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(content))
	}

	first := content[0].(map[string]any)
	second := content[1].(map[string]any)

	if first["type"] != "tool_result" {
		t.Errorf("first block: got %v, want tool_result", first["type"])
	}
	if second["type"] != "text" {
		t.Errorf("second block: got %v, want text", second["type"])
	}
}

// TestConvertRequest_RedactedThinking verifies redacted reasoning round-trips
// as a redacted_thinking input block, not a thinking block with a bogus
// signature.
func TestConvertRequest_RedactedThinking(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{Text: "step", Signature: "SIG"}),
				provider.ReasoningContent(provider.Reasoning{Signature: "BLOB", Redacted: true}),
				provider.TextContent("answer"),
			},
		},
	}

	body := requestBody(t, completer, messages, nil)

	msgs := body["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)

	var types []string
	for _, c := range content {
		types = append(types, c.(map[string]any)["type"].(string))
	}

	want := []string{"thinking", "redacted_thinking", "text"}
	if !slices.Equal(types, want) {
		t.Fatalf("blocks: got %v, want %v", types, want)
	}

	redacted := content[1].(map[string]any)
	if redacted["data"] != "BLOB" {
		t.Errorf("data: got %v", redacted["data"])
	}
}

// TestConvertRequest_CompactionDefaultTrigger verifies compaction without a
// threshold omits the trigger so the upstream default applies.
func TestConvertRequest_CompactionDefaultTrigger(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{provider.UserMessage("hi")}

	body := requestBody(t, completer, messages, &provider.CompleteOptions{
		CompactionOptions: &provider.CompactionOptions{},
	})

	cm, ok := body["context_management"].(map[string]any)
	if !ok {
		t.Fatal("expected context_management")
	}

	edits := cm["edits"].([]any)
	if len(edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(edits))
	}

	edit := edits[0].(map[string]any)
	if edit["type"] != "compact_20260112" {
		t.Errorf("edit type: got %v", edit["type"])
	}
	if _, present := edit["trigger"]; present {
		t.Errorf("expected trigger omitted, got %v", edit["trigger"])
	}
}

func TestConvertRequest_CompactionExplicitTrigger(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{provider.UserMessage("hi")}

	body := requestBody(t, completer, messages, &provider.CompleteOptions{
		CompactionOptions: &provider.CompactionOptions{Threshold: 50000},
	})

	cm := body["context_management"].(map[string]any)
	edit := cm["edits"].([]any)[0].(map[string]any)

	trigger, ok := edit["trigger"].(map[string]any)
	if !ok {
		t.Fatal("expected trigger")
	}
	if trigger["value"] != float64(50000) {
		t.Errorf("trigger value: got %v", trigger["value"])
	}
}

// TestConvertRequest_SystemPlacement verifies a leading system message maps to
// the top-level system field and a mid-conversation one stays in messages.
func TestConvertRequest_SystemPlacement(t *testing.T) {
	completer, _ := NewCompleter("http://localhost", "claude-test")

	messages := []provider.Message{
		provider.SystemMessage("be helpful"),
		provider.UserMessage("hi"),
		provider.SystemMessage("now be terse"),
	}

	body := requestBody(t, completer, messages, nil)

	system, ok := body["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("expected 1 top-level system block, got %v", body["system"])
	}
	if system[0].(map[string]any)["text"] != "be helpful" {
		t.Errorf("system text: got %v", system[0])
	}

	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].(map[string]any)["role"] != "system" {
		t.Errorf("expected mid-conversation system role, got %v", msgs[1].(map[string]any)["role"])
	}
}

// TestCompleterStreamingStopSequence verifies the matched stop sequence is
// surfaced on the completion.
func TestCompleterStreamingStopSequence(t *testing.T) {
	events := []string{
		sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"END"},"usage":{"output_tokens":4}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte(e))
		}
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "claude-test")
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
	if result.StopSequence != "END" {
		t.Errorf("stop sequence: got %q, want END", result.StopSequence)
	}
	if result.Status != "" {
		t.Errorf("status: got %q, want empty", result.Status)
	}
}

// TestCompleterStreamingToolArgs verifies streamed tool arguments are not
// polluted by the empty-input normalization.
func TestCompleterStreamingToolArgs(t *testing.T) {
	events := []string{
		sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Bern\"}"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":8}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte(e))
		}
	}))
	defer server.Close()

	completer, err := NewCompleter(server.URL, "claude-test")
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

	calls := acc.Result().Message.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Arguments != `{"city":"Bern"}` {
		t.Errorf("arguments: got %q", calls[0].Arguments)
	}
}

// TestToUsage_CacheInclusiveInputTokens verifies the intermediate Usage uses a
// cache-inclusive InputTokens total. Anthropic reports input_tokens excluding
// cached tokens, so the mapping folds cache read/creation back into InputTokens
// while still exposing the cached subset in the cache fields.
func TestToUsage_CacheInclusiveInputTokens(t *testing.T) {
	usage := toUsage(anthropic.BetaUsage{
		InputTokens:              10,
		OutputTokens:             7,
		CacheReadInputTokens:     40,
		CacheCreationInputTokens: 50,
	})

	if usage == nil {
		t.Fatal("expected usage")
	}

	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (10 fresh + 40 read + 50 creation)", usage.InputTokens)
	}
	if usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", usage.OutputTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Errorf("CacheReadInputTokens = %d, want 40", usage.CacheReadInputTokens)
	}
	if usage.CacheCreationInputTokens != 50 {
		t.Errorf("CacheCreationInputTokens = %d, want 50", usage.CacheCreationInputTokens)
	}

	if usage.CacheReadInputTokens+usage.CacheCreationInputTokens > usage.InputTokens {
		t.Errorf("cache tokens (%d+%d) exceed InputTokens (%d)",
			usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.InputTokens)
	}
}

func TestToUsage_ZeroReturnsNil(t *testing.T) {
	if usage := toUsage(anthropic.BetaUsage{}); usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
}

// TestToUsage_ReasoningTokens verifies thinking tokens map to ReasoningTokens
// as a subset of the reasoning-inclusive OutputTokens.
func TestToUsage_ReasoningTokens(t *testing.T) {
	usage := toUsage(anthropic.BetaUsage{
		InputTokens:         10,
		OutputTokens:        30,
		OutputTokensDetails: anthropic.BetaOutputTokensDetails{ThinkingTokens: 12},
	})

	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.OutputTokens != 30 {
		t.Errorf("OutputTokens = %d, want 30 (thinking-inclusive)", usage.OutputTokens)
	}
	if usage.ReasoningTokens != 12 {
		t.Errorf("ReasoningTokens = %d, want 12", usage.ReasoningTokens)
	}
	if usage.ReasoningTokens > usage.OutputTokens {
		t.Errorf("reasoning tokens (%d) exceed OutputTokens (%d)", usage.ReasoningTokens, usage.OutputTokens)
	}
}
