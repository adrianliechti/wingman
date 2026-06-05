package anthropic

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestStreamingAccumulatorEmitsCacheUsage(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			InputTokens:              0,
			CacheReadInputTokens:     40,
			CacheCreationInputTokens: 50,
		},
	})
	if err != nil {
		t.Fatalf("add completion: %v", err)
	}

	err = acc.Complete()
	if err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("expected stream events, got %d", len(events))
	}

	start := events[0]
	if start.Type != StreamEventMessageStart {
		t.Fatalf("expected first event %q, got %q", StreamEventMessageStart, start.Type)
	}
	if start.Message == nil {
		t.Fatal("expected message_start payload")
	}
	if start.Message.Usage.CacheReadInputTokens != 40 {
		t.Fatalf("expected message_start cache read input tokens 40, got %d", start.Message.Usage.CacheReadInputTokens)
	}
	if start.Message.Usage.CacheCreationInputTokens != 50 {
		t.Fatalf("expected message_start cache creation input tokens 50, got %d", start.Message.Usage.CacheCreationInputTokens)
	}

	var delta *DeltaUsage
	for _, event := range events {
		if event.Type == StreamEventMessageDelta {
			delta = event.DeltaUsage
		}
	}
	if delta == nil {
		t.Fatal("expected message_delta usage")
	}
	if delta.CacheReadInputTokens != 40 {
		t.Fatalf("expected message_delta cache read input tokens 40, got %d", delta.CacheReadInputTokens)
	}
	if delta.CacheCreationInputTokens != 50 {
		t.Fatalf("expected message_delta cache creation input tokens 50, got %d", delta.CacheCreationInputTokens)
	}
}

func TestStreamingAccumulatorInterleavedToolCalls(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	add := func(content provider.Content) {
		t.Helper()
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}}}); err != nil {
			t.Fatalf("add completion: %v", err)
		}
	}

	add(provider.ToolCallContent(provider.ToolCall{ID: "tool_a", Name: "get_weather"}))
	add(provider.ToolCallContent(provider.ToolCall{ID: "tool_b", Name: "get_time"}))
	add(provider.ToolCallContent(provider.ToolCall{ID: "tool_a", Arguments: `{"city":`}))
	add(provider.ToolCallContent(provider.ToolCall{ID: "tool_b", Arguments: `{"zone":"UTC"}`}))
	add(provider.ToolCallContent(provider.ToolCall{ID: "tool_a", Arguments: `"Bern"}`}))

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	starts := map[int]string{}
	args := map[int]string{}
	stops := 0

	for _, event := range events {
		switch event.Type {
		case StreamEventContentBlockStart:
			if _, exists := starts[event.Index]; exists {
				t.Fatalf("duplicate content_block_start for index %d", event.Index)
			}
			starts[event.Index] = event.ContentBlock.ID
		case StreamEventContentBlockDelta:
			if event.Delta.Type == "input_json_delta" {
				args[event.Index] += event.Delta.PartialJSON
			}
		case StreamEventContentBlockStop:
			stops++
		}
	}

	if len(starts) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %d", len(starts))
	}
	if stops != 2 {
		t.Fatalf("expected 2 content_block_stop events, got %d", stops)
	}
	if starts[0] != "tool_a" || starts[1] != "tool_b" {
		t.Fatalf("unexpected block ids: %v", starts)
	}
	if args[0] != `{"city":"Bern"}` {
		t.Errorf("tool_a args: got %q", args[0])
	}
	if args[1] != `{"zone":"UTC"}` {
		t.Errorf("tool_b args: got %q", args[1])
	}
}

func TestStreamingAccumulatorSplitsThinkingBlocks(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	add := func(content provider.Content) {
		t.Helper()
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}}}); err != nil {
			t.Fatalf("add completion: %v", err)
		}
	}

	add(provider.ReasoningContent(provider.Reasoning{Text: "first thought"}))
	add(provider.ReasoningContent(provider.Reasoning{Signature: "SIG_1"}))
	add(provider.TextContent("hello"))
	add(provider.ReasoningContent(provider.Reasoning{Text: "second thought"}))
	add(provider.ReasoningContent(provider.Reasoning{Signature: "SIG_2"}))

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	thinking := map[int]string{}
	signatures := map[int]string{}

	for _, event := range events {
		if event.Type != StreamEventContentBlockDelta {
			continue
		}
		switch event.Delta.Type {
		case "thinking_delta":
			thinking[event.Index] += event.Delta.Thinking
		case "signature_delta":
			signatures[event.Index] += event.Delta.Signature
		}
	}

	if len(thinking) != 2 || len(signatures) != 2 {
		t.Fatalf("expected 2 thinking blocks, got thinking=%v signatures=%v", thinking, signatures)
	}
	if thinking[0] != "first thought" || signatures[0] != "SIG_1" {
		t.Errorf("block 0: got %q / %q", thinking[0], signatures[0])
	}
	if thinking[2] != "second thought" || signatures[2] != "SIG_2" {
		t.Errorf("block 2: got %q / %q", thinking[2], signatures[2])
	}
}
