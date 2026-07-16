package chat

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// clientToolCall mirrors how OpenAI SDKs rebuild tool calls from chunks:
// keyed by index, id/type assigned, name and arguments concatenated with +=.
type clientToolCall struct {
	ID   string
	Type ToolType
	Name string
	Args string
}

func accumulateLikeClient(t *testing.T, chunks []*ChatCompletion) []clientToolCall {
	t.Helper()

	var calls []clientToolCall

	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.Delta == nil {
				continue
			}

			for _, tc := range choice.Delta.ToolCalls {
				if tc.Index == nil {
					t.Fatalf("tool call chunk without index: %+v", tc)
				}

				for len(calls) <= *tc.Index {
					calls = append(calls, clientToolCall{})
				}

				call := &calls[*tc.Index]

				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Type != "" {
					call.Type = tc.Type
				}
				if tc.Function != nil {
					call.Name += tc.Function.Name
					call.Args += tc.Function.Arguments
				}
			}
		}
	}

	return calls
}

func collectChunks(acc **StreamingAccumulator) *[]*ChatCompletion {
	chunks := &[]*ChatCompletion{}

	*acc = NewStreamingAccumulator("model", func(event StreamEvent) error {
		if event.Chunk != nil {
			*chunks = append(*chunks, event.Chunk)
		}
		return nil
	})

	return chunks
}

func toolCallDelta(call provider.ToolCall) provider.Completion {
	return provider.Completion{
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(call)},
		},
	}
}

// OpenAI streams id/type/name only on a call's first chunk. Clients
// accumulate name/arguments with +=, so repeating them corrupts the call.
func TestStreamingAccumulatorToolCallChunkShape(t *testing.T) {
	var acc *StreamingAccumulator
	chunks := collectChunks(&acc)

	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_1", Name: "get_weather"})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_1", Arguments: `{"location":`})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_1", Arguments: `"Paris"}`})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Complete(false); err != nil {
		t.Fatal(err)
	}

	var toolChunks []ToolCall
	for _, chunk := range *chunks {
		for _, choice := range chunk.Choices {
			if choice.Delta != nil {
				toolChunks = append(toolChunks, choice.Delta.ToolCalls...)
			}
		}
	}

	if len(toolChunks) != 3 {
		t.Fatalf("expected 3 tool call chunks, got %d: %+v", len(toolChunks), toolChunks)
	}

	first := toolChunks[0]
	if first.ID != "call_1" || first.Type != ToolTypeFunction || first.Function == nil || first.Function.Name != "get_weather" {
		t.Errorf("first chunk must carry id/type/name: %+v", first)
	}

	for i, tc := range toolChunks[1:] {
		if tc.ID != "" || tc.Type != "" || (tc.Function != nil && tc.Function.Name != "") {
			t.Errorf("fragment %d must not repeat id/type/name: %+v", i+1, tc)
		}
		if tc.Index == nil || *tc.Index != 0 {
			t.Errorf("fragment %d must carry index 0: %+v", i+1, tc)
		}
	}

	calls := accumulateLikeClient(t, *chunks)
	if len(calls) != 1 {
		t.Fatalf("client rebuilt %d calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "get_weather" || calls[0].Args != `{"location":"Paris"}` {
		t.Errorf("client rebuilt call wrong: %+v", calls[0])
	}
}

func TestStreamingAccumulatorParallelToolCallIndices(t *testing.T) {
	var acc *StreamingAccumulator
	chunks := collectChunks(&acc)

	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_1", Name: "get_weather", Arguments: `{"location":"Paris"}`})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_2", Name: "get_time", Arguments: `{"zone":"CET"}`})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Complete(false); err != nil {
		t.Fatal(err)
	}

	calls := accumulateLikeClient(t, *chunks)
	if len(calls) != 2 {
		t.Fatalf("client rebuilt %d calls, want 2: %+v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "get_weather" || calls[0].Args != `{"location":"Paris"}` {
		t.Errorf("call 0 wrong: %+v", calls[0])
	}
	if calls[1].ID != "call_2" || calls[1].Name != "get_time" || calls[1].Args != `{"zone":"CET"}` {
		t.Errorf("call 1 wrong: %+v", calls[1])
	}
}

// A call that streams no argument bytes must still deliver "{}" — clients
// rebuild arguments purely from deltas and JSON.parse("") fails.
func TestStreamingAccumulatorEmitsEmptyArgumentsChunk(t *testing.T) {
	var acc *StreamingAccumulator
	chunks := collectChunks(&acc)

	if err := acc.Add(toolCallDelta(provider.ToolCall{ID: "call_1", Name: "get_time"})); err != nil {
		t.Fatal(err)
	}
	if err := acc.Complete(false); err != nil {
		t.Fatal(err)
	}

	calls := accumulateLikeClient(t, *chunks)
	if len(calls) != 1 {
		t.Fatalf("client rebuilt %d calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].Args != "{}" {
		t.Errorf("arguments: got %q, want {}", calls[0].Args)
	}
}

func TestStreamingAccumulatorUsageAlwaysIncludesPromptTokensDetails(t *testing.T) {
	var usage *Usage
	acc := NewStreamingAccumulator("model", func(event StreamEvent) error {
		if event.Type == StreamEventUsage && event.Chunk != nil {
			usage = event.Chunk.Usage
		}
		return nil
	})

	if err := acc.Add(provider.Completion{
		Usage: &provider.Usage{InputTokens: 10, OutputTokens: 5},
	}); err != nil {
		t.Fatalf("add usage: %v", err)
	}
	if err := acc.Complete(true); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	if usage == nil || usage.PromptTokensDetails == nil {
		t.Fatal("expected prompt_tokens_details to be present even with no cache hit")
	}
	if usage.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("expected cached tokens 0, got %d", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestStreamingAccumulatorUsageIncludesCachedTokens(t *testing.T) {
	var usage *Usage
	acc := NewStreamingAccumulator("model", func(event StreamEvent) error {
		if event.Type == StreamEventUsage && event.Chunk != nil {
			usage = event.Chunk.Usage
		}
		return nil
	})

	err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			InputTokens:          100,
			OutputTokens:         20,
			CacheReadInputTokens: 80,
		},
	})
	if err != nil {
		t.Fatalf("add usage: %v", err)
	}

	err = acc.Complete(true)
	if err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	if usage == nil {
		t.Fatal("expected usage chunk")
	}
	if usage.PromptTokensDetails == nil {
		t.Fatal("expected prompt token details")
	}
	if usage.PromptTokensDetails.CachedTokens != 80 {
		t.Fatalf("expected cached tokens 80, got %d", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestStreamingAccumulatorUsageIncludesReasoningAndTotal(t *testing.T) {
	var usage *Usage
	acc := NewStreamingAccumulator("model", func(event StreamEvent) error {
		if event.Type == StreamEventUsage && event.Chunk != nil {
			usage = event.Chunk.Usage
		}
		return nil
	})

	if err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			InputTokens:          100,
			OutputTokens:         30,
			ReasoningTokens:      12,
			CacheReadInputTokens: 80,
		},
	}); err != nil {
		t.Fatalf("add usage: %v", err)
	}
	if err := acc.Complete(true); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	if usage == nil {
		t.Fatal("expected usage chunk")
	}
	if usage.PromptTokens != 100 {
		t.Fatalf("expected prompt_tokens 100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 30 {
		t.Fatalf("expected completion_tokens 30 (reasoning-inclusive), got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 130 {
		t.Fatalf("expected total_tokens 130 (prompt+completion), got %d", usage.TotalTokens)
	}
	if usage.CompletionTokensDetails == nil {
		t.Fatal("expected completion_tokens_details")
	}
	if usage.CompletionTokensDetails.ReasoningTokens != 12 {
		t.Fatalf("expected reasoning_tokens 12, got %d", usage.CompletionTokensDetails.ReasoningTokens)
	}
}
