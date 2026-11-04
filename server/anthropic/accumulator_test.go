package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestStreamingAccumulatorTrailingSignaturePreservesFinalText(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_fixture", "gemini-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	for _, content := range []provider.Content{
		provider.TextContent("hello "),
		provider.TextContent("world"),
		provider.ReasoningContent(provider.Reasoning{ID: "gemsig_fixture", Signature: "opaque-state"}),
	} {
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	open := map[int]bool{}
	text, signature := "", ""
	textIndex, lastClosed := -1, -1
	for _, event := range events {
		switch event.Type {
		case StreamEventContentBlockStart:
			open[event.Index] = true
			if event.ContentBlock.Type == "text" {
				if textIndex != -1 {
					t.Fatal("answer was split across text blocks")
				}
				textIndex = event.Index
			}
		case StreamEventContentBlockDelta:
			if !open[event.Index] {
				t.Fatal("delta outside an open block")
			}
			text += event.Delta.Text
			if event.Delta.Type == "signature_delta" {
				_, signature = decodeSignature(event.Delta.Signature)
			}
		case StreamEventContentBlockStop:
			delete(open, event.Index)
			lastClosed = event.Index
		}
	}
	if len(open) != 0 || lastClosed != textIndex || text != "hello world" || signature != "opaque-state" {
		t.Fatalf("lost final answer or signature: last=%d textIndex=%d text=%q signature=%q open=%v", lastClosed, textIndex, text, signature, open)
	}
}

func TestStreamingAccumulatorEmitsCacheUsage(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			// Cache-inclusive intermediate total: 10 fresh + 40 read + 50 write.
			InputTokens:              100,
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
	if start.Message.Usage.InputTokens != 10 {
		t.Fatalf("expected message_start input tokens 10 (cache-exclusive), got %d", start.Message.Usage.InputTokens)
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
	if delta.InputTokens != 10 {
		t.Fatalf("expected message_delta input tokens 10 (cache-exclusive), got %d", delta.InputTokens)
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
	lifecycle := map[int]string{}
	stops := 0

	for _, event := range events {
		switch event.Type {
		case StreamEventContentBlockStart:
			if _, exists := starts[event.Index]; exists {
				t.Fatalf("duplicate content_block_start for index %d", event.Index)
			}
			starts[event.Index] = event.ContentBlock.ID
			lifecycle[event.Index] = "open"
		case StreamEventContentBlockDelta:
			if lifecycle[event.Index] != "open" {
				t.Fatalf("content_block_delta for index %d while block is %q", event.Index, lifecycle[event.Index])
			}
			if event.Delta.Type == "input_json_delta" {
				args[event.Index] += event.Delta.PartialJSON
			}
		case StreamEventContentBlockStop:
			if lifecycle[event.Index] != "open" {
				t.Fatalf("content_block_stop for index %d while block is %q", event.Index, lifecycle[event.Index])
			}
			lifecycle[event.Index] = "stopped"
			stops++
		}
	}

	if len(starts) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %d", len(starts))
	}
	if stops != 2 {
		t.Fatalf("expected 2 content_block_stop events, got %d", stops)
	}
	for index, state := range lifecycle {
		if state != "stopped" {
			t.Errorf("block %d lifecycle ended in %q", index, state)
		}
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

	add(provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("first thought")}))
	add(provider.ReasoningContent(provider.Reasoning{Signature: "SIG_1"}))
	add(provider.TextContent("hello"))
	add(provider.ReasoningContent(provider.Reasoning{Content: provider.TextParts("second thought")}))
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

func TestStreamingAccumulatorRedactedThinking(t *testing.T) {
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

	add(provider.ReasoningContent(provider.Reasoning{Signature: "BLOB", Redacted: true}))
	add(provider.TextContent("hello"))

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	var redacted *ContentBlock
	redactedStopped := false
	redactedIndex := -1

	for _, event := range events {
		switch event.Type {
		case StreamEventContentBlockStart:
			if event.ContentBlock.Type == "redacted_thinking" {
				redacted = event.ContentBlock
				redactedIndex = event.Index
			}
		case StreamEventContentBlockStop:
			if event.Index == redactedIndex && redacted != nil {
				redactedStopped = true
			}
		}
	}

	if redacted == nil {
		t.Fatal("expected redacted_thinking content_block_start")
	}
	if redacted.Data != "BLOB" {
		t.Errorf("data: got %q, want BLOB", redacted.Data)
	}
	if !redactedStopped {
		t.Error("expected content_block_stop for redacted_thinking block")
	}
}

func TestStreamingAccumulatorStopSequence(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	if err := acc.Add(provider.Completion{
		Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("partial")}},
	}); err != nil {
		t.Fatalf("add completion: %v", err)
	}

	if err := acc.Add(provider.Completion{StopSequence: "END"}); err != nil {
		t.Fatalf("add stop: %v", err)
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	var delta *MessageDelta
	for _, event := range events {
		if event.Type == StreamEventMessageDelta {
			delta = event.MessageDelta
		}
	}

	if delta == nil {
		t.Fatal("expected message_delta event")
	}
	if delta.StopReason != StopReasonStopSequence {
		t.Errorf("stop reason: got %q, want stop_sequence", delta.StopReason)
	}
	if delta.StopSequence == nil || *delta.StopSequence != "END" {
		t.Errorf("stop sequence: got %v, want END", delta.StopSequence)
	}
}

// Output and thinking tokens must survive the streaming round-trip: the final
// message_delta carries output_tokens and reports the reasoning subset in
// output_tokens_details.thinking_tokens, while input_tokens stays cache-exclusive.
func TestStreamingAccumulatorEmitsThinkingUsage(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "claude-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			// Cache-inclusive intermediate total: 10 fresh + 40 read + 50 write.
			InputTokens:              100,
			OutputTokens:             30,
			ReasoningTokens:          new(12),
			CacheReadInputTokens:     40,
			CacheCreationInputTokens: 50,
		},
	})
	if err != nil {
		t.Fatalf("add completion: %v", err)
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete stream: %v", err)
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
	if delta.InputTokens != 10 {
		t.Fatalf("expected message_delta input tokens 10 (cache-exclusive), got %d", delta.InputTokens)
	}
	if delta.OutputTokens != 30 {
		t.Fatalf("expected message_delta output tokens 30 (thinking-inclusive), got %d", delta.OutputTokens)
	}
	if delta.OutputTokensDetails == nil {
		t.Fatal("expected message_delta output_tokens_details")
	}
	if delta.OutputTokensDetails.ThinkingTokens != 12 {
		t.Fatalf("expected message_delta thinking tokens 12, got %d", delta.OutputTokensDetails.ThinkingTokens)
	}
}

func TestStreamingAccumulatorSummaryReasoning(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_123", "gpt-test", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})

	add := func(content provider.Content) {
		t.Helper()
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}}}); err != nil {
			t.Fatalf("add completion: %v", err)
		}
	}

	add(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("summarized ")}))
	add(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("thought")}))
	add(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Signature: "ENC"}))
	add(provider.TextContent("hello"))

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

	if len(thinking) != 1 {
		t.Fatalf("expected 1 thinking block, got %v", thinking)
	}
	if thinking[0] != "summarized thought" {
		t.Errorf("thinking: got %q, want %q", thinking[0], "summarized thought")
	}
	if len(signatures) != 1 || signatures[0] == "" {
		t.Errorf("expected signature_delta on the summary block, got %v", signatures)
	}
}

// Message items arrive with IDs from OpenAI (commentary and final answer) or
// from a resumed Claude turn. Streaming emits one text block per item, as the
// non-streaming response does.
func TestStreamingAccumulatorSplitsTextBlocksPerMessageItem(t *testing.T) {
	var events []StreamEvent
	acc := NewStreamingAccumulator("msg_test", "model", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	add := func(parts ...provider.Content) {
		t.Helper()
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: parts}}); err != nil {
			t.Fatal(err)
		}
	}
	add(provider.Content{MessageID: "msg_1", Phase: provider.MessagePhaseCommentary})
	add(provider.Content{MessageID: "msg_1", Text: "Looking it up."})
	add(provider.Content{MessageID: "msg_2", Phase: provider.MessagePhaseFinalAnswer})
	add(provider.Content{MessageID: "msg_2", Text: "42"})
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}

	var blocks []string
	texts := map[int]string{}
	for _, e := range events {
		switch e.Type {
		case StreamEventContentBlockStart:
			blocks = append(blocks, e.ContentBlock.Type)
		case StreamEventContentBlockDelta:
			texts[e.Index] += e.Delta.Text
		}
	}
	if len(blocks) != 2 || blocks[0] != "text" || blocks[1] != "text" || texts[0] != "Looking it up." || texts[1] != "42" {
		t.Fatalf("streamed blocks = %v %v, want one text block per message item", blocks, texts)
	}

	content := toContentBlocks(acc.Result().Message.Content)
	if len(content) != 2 || *content[0].Text != "Looking it up." || *content[1].Text != "42" {
		t.Fatalf("non-streaming blocks = %+v, want the same two text blocks", content)
	}
}

// Cache counts may arrive after the cache-inclusive input total. A stale
// cache-exclusive count must not cause those cached tokens to be billed twice.
func TestStreamingAccumulatorLateCacheUsage(t *testing.T) {
	var usage *DeltaUsage
	acc := NewStreamingAccumulator("msg_test", "model", func(event StreamEvent) error {
		if event.Type == StreamEventMessageDelta {
			usage = event.DeltaUsage
		}
		return nil
	})
	for _, delta := range []provider.Usage{
		{InputTokens: 100, OutputTokens: 1},
		{CacheReadInputTokens: 60, CacheCreationInputTokens: 40},
		{OutputTokens: 10},
	} {
		if err := acc.Add(provider.Completion{Usage: &delta}); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.InputTokens != 0 || usage.CacheReadInputTokens != 60 || usage.CacheCreationInputTokens != 40 || usage.OutputTokens != 10 {
		t.Fatalf("usage lost or double-counted: %+v", usage)
	}
}

func TestStreamingAccumulatorEmptyStreamStartsMessage(t *testing.T) {
	var types []StreamEventType
	acc := NewStreamingAccumulator("msg_empty", "model", func(event StreamEvent) error {
		types = append(types, event.Type)
		return nil
	})
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 || types[0] != StreamEventMessageStart || types[1] != StreamEventMessageDelta || types[2] != StreamEventMessageStop {
		t.Fatalf("events = %v, want [message_start message_delta message_stop]", types)
	}
}

func TestStreamingAccumulatorTranslatesCrossDialectToolCalls(t *testing.T) {
	var startedName string
	var deltaJSON string
	acc := NewStreamingAccumulator("msg_patch", "model", func(event StreamEvent) error {
		switch event.Type {
		case StreamEventContentBlockStart:
			startedName = event.ContentBlock.Name
		case StreamEventContentBlockDelta:
			deltaJSON += event.Delta.PartialJSON
		}
		return nil
	})
	for _, chunk := range []provider.ToolCall{
		{
			ID:        "call_patch",
			Kind:      provider.ToolKindTextEditor,
			Name:      "apply_patch",
			Arguments: `{"type":"create_file",`,
		},
		{
			ID:        "call_patch",
			Arguments: `"path":"hello.txt","diff":"+hello"}`,
		},
	} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(chunk)},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatal(err)
	}
	if startedName != "str_replace_based_edit_tool" {
		t.Fatalf("startedName = %q, want str_replace_based_edit_tool", startedName)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(deltaJSON), &parsed); err != nil || parsed["command"] != "create" || parsed["path"] != "hello.txt" {
		t.Fatalf("deltaJSON = %q (err=%v, parsed=%+v)", deltaJSON, err, parsed)
	}

	// Ordinary function tools named apply_patch (without ToolKindTextEditor)
	// must stream raw argument fragments untranslated.
	startedName = ""
	deltaJSON = ""
	fnAcc := NewStreamingAccumulator("msg_fn", "model", func(event StreamEvent) error {
		switch event.Type {
		case StreamEventContentBlockStart:
			startedName = event.ContentBlock.Name
		case StreamEventContentBlockDelta:
			deltaJSON += event.Delta.PartialJSON
		}
		return nil
	})
	for _, chunk := range []provider.ToolCall{
		{ID: "call_fn", Name: "apply_patch", Arguments: `{"patch":`},
		{ID: "call_fn", Arguments: `"custom"}`},
	} {
		if err := fnAcc.Add(provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(chunk)},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fnAcc.Complete(); err != nil {
		t.Fatal(err)
	}
	if startedName != "apply_patch" || deltaJSON != `{"patch":"custom"}` {
		t.Fatalf("ordinary function tool: startedName=%q deltaJSON=%q", startedName, deltaJSON)
	}
}
