package gemini

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func collectStream(t *testing.T, chunks ...provider.Completion) []GenerateContentResponse {
	t.Helper()

	var out []GenerateContentResponse

	acc := NewStreamingAccumulator("resp_1", "model", func(r GenerateContentResponse) error {
		out = append(out, r)
		return nil
	})

	for _, c := range chunks {
		if err := acc.Add(c); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}

	return out
}

func assistantChunk(content ...provider.Content) provider.Completion {
	return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: content}}
}

func partsOf(r GenerateContentResponse) []*Part {
	if len(r.Candidates) == 0 || r.Candidates[0].Content == nil {
		return nil
	}
	return r.Candidates[0].Content.Parts
}

// Text streamed before a function call must not be repeated in the final
// chunk — clients concatenate text parts across chunks.
func TestStreamingFinalChunkCarriesOnlyFunctionCalls(t *testing.T) {
	out := collectStream(t,
		assistantChunk(provider.TextContent("Let me check. ")),
		assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: "thinking"})),
		assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "get_weather", Arguments: `{"city":"Bern"}`})),
	)

	if len(out) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(out))
	}

	final := partsOf(out[2])

	if len(final) != 1 || final[0].FunctionCall == nil {
		t.Fatalf("final chunk should hold only the function call, got %+v", final)
	}

	if final[0].FunctionCall.Name != "get_weather" || final[0].FunctionCall.Args["city"] != "Bern" {
		t.Errorf("function call: %+v", final[0].FunctionCall)
	}

	if out[2].Candidates[0].FinishReason != FinishReasonStop {
		t.Errorf("finish reason: %s", out[2].Candidates[0].FinishReason)
	}
}

// OpenAI-backed reasoning arrives as Summary deltas; they must stream as
// thought parts just like Text deltas do.
func TestStreamingReasoningSummaryBecomesThought(t *testing.T) {
	out := collectStream(t,
		assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: "thinking summary"})),
		assistantChunk(provider.TextContent("Answer")),
	)

	if len(out) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(out))
	}

	thought := partsOf(out[0])
	if len(thought) != 1 || !thought[0].Thought || thought[0].Text != "thinking summary" {
		t.Fatalf("expected a thought part, got %+v", thought)
	}

	if final := partsOf(out[2]); len(final) != 0 {
		t.Errorf("text-only reply must not repeat content in the final chunk: %+v", final)
	}
}

// Gemini attaches thought signatures to the first non-thought part; the
// signature-only reasoning content that precedes text must ride on it.
func TestStreamingThoughtSignatureAttachesToText(t *testing.T) {
	out := collectStream(t,
		assistantChunk(
			provider.ReasoningContent(provider.Reasoning{ID: "gemsig_1", Signature: "SIG"}),
			provider.TextContent("Answer"),
		),
	)

	parts := partsOf(out[0])
	if len(parts) != 1 || parts[0].Text != "Answer" || string(parts[0].ThoughtSignature) != "SIG" {
		t.Fatalf("expected text part carrying the signature, got %+v", parts)
	}
}
