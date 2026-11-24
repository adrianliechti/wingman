package gemini

import (
	"encoding/json/v2"
	"errors"
	"slices"
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

// signedBy reports the reasoning item ID and signature that a part carries.
func signedBy(part *Part) string {
	id, signature := provider.UnwrapReasoningID(string(part.ThoughtSignature))
	return id + "=" + signature
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
		assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("thinking")})),
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
		assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("thinking summary")})),
		assistantChunk(provider.TextContent("Answer")),
	)

	if len(out) != 2 {
		t.Fatalf("expected two content-bearing chunks, got %d", len(out))
	}
	thought := partsOf(out[0])
	if len(thought) != 1 || !thought[0].Thought || thought[0].Text != "thinking summary" {
		t.Fatalf("expected a thought part, got %+v", thought)
	}

	if final := partsOf(out[1]); len(final) != 1 || final[0].Text != "Answer" || out[1].Candidates[0].FinishReason != FinishReasonStop {
		t.Errorf("final chunk must carry the answer and STOP: %+v", out[1])
	}
}

func TestStreamingChatHistory(t *testing.T) {
	for _, finish := range []provider.StopReason{provider.StopReasonEndTurn, provider.StopReasonMaxTokens} {
		out := collectStream(t, assistantChunk(provider.TextContent("one")), assistantChunk(provider.TextContent("two")),
			provider.Completion{StopReason: finish, Usage: &provider.Usage{InputTokens: 3, OutputTokens: 2}})
		var history []string
		for i, chunk := range out {
			// chats._validate_response rejects the whole turn if any chunk
			// lacks candidate content or contains an empty part.
			if len(partsOf(chunk)) == 0 || chunk.Candidates[0].Content.Role != "model" {
				t.Fatalf("chunk %d would discard chat history: %+v", i, chunk)
			}
			for _, p := range partsOf(chunk) {
				history = append(history, p.Text)
			}
			if i < len(out)-1 && chunk.Candidates[0].FinishReason != "" {
				t.Fatalf("early finish in chunk %d", i)
			}
		}
		last := out[len(out)-1]
		if !slices.Equal(history, []string{"one", "two"}) || last.UsageMetadata.TotalTokenCount != 5 || last.Candidates[0].FinishReason != toFinishReason(&provider.Completion{StopReason: finish}) {
			t.Fatalf("incorrect terminal chunk or history: %+v %v", last, history)
		}
	}
}

func TestStreamingErrorPreservesTextWithoutFinishing(t *testing.T) {
	var out []GenerateContentResponse
	acc := NewStreamingAccumulator("id", "model", func(r GenerateContentResponse) error { out = append(out, r); return nil })
	if err := acc.Add(assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "unsigned"}), provider.TextContent("partial"))); err != nil {
		t.Fatal(err)
	}
	if err := acc.Error(errors.New("interrupted")); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || len(partsOf(out[0])) != 1 || partsOf(out[0])[0].Text != "partial" || out[0].Candidates[0].FinishReason != "" || out[1].Error == nil {
		t.Fatalf("expected partial text followed by an error: %+v", out)
	}
}

func TestStreamingLateSignatureOnParallelCall(t *testing.T) {
	out := collectStream(t,
		assistantChunk(provider.TextContent("before")),
		assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "r", Summary: provider.TextParts("checking")})),
		assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "first", Name: "lookup"})),
		assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "second", Name: "lookup", Arguments: "{}"})),
		assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "first", Arguments: "{\"city\":\"Bern\"}"})),
		assistantChunk(provider.TextContent("after")),
		provider.Completion{ReasoningSnapshots: provider.ReasoningSnapshots{"r": {
			Signature: "/wCA", Summary: provider.TextParts("checking"),
		}}},
	)
	last := partsOf(out[len(out)-1])
	if len(last) != 3 || last[0].FunctionCall == nil || last[0].FunctionCall.ID != "first" || last[0].FunctionCall.Args["city"] != "Bern" ||
		signedBy(last[0]) != "r=/wCA" || last[1].FunctionCall == nil || last[1].FunctionCall.ID != "second" ||
		len(last[1].ThoughtSignature) != 0 || last[2].Text != "after" {
		t.Fatalf("final parallel calls, signature or part order: %+v", last)
	}
	if len(out) != 3 || len(partsOf(out[0])) != 1 || partsOf(out[0])[0].Text != "before" || len(partsOf(out[1])) != 1 || partsOf(out[1])[0].Text != "checking" {
		t.Fatalf("incorrect content before calls: %+v", out)
	}
}

// Gemini clients replay candidate contents in the order the stream delivered them.
func TestTerminalSignaturesKeepTheirPositionOnReplay(t *testing.T) {
	reasoning := func(id string) provider.Content { return provider.ReasoningContent(provider.Reasoning{ID: id}) }
	chunks := []provider.Completion{
		assistantChunk(reasoning("r1")), assistantChunk(provider.TextContent("answer")),
		assistantChunk(reasoning("r2")), assistantChunk(reasoning("r3")),
		assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "call", Name: "lookup", Arguments: "{}"})),
		{ReasoningSnapshots: provider.ReasoningSnapshots{"r1": {Signature: "enc1"}, "r2": {Signature: "enc2"}, "r3": {Signature: "enc3"}}},
	}
	var accumulated provider.CompletionAccumulator
	for _, c := range chunks {
		accumulated.Add(c)
	}
	var streamed []*Content
	for _, c := range collectStream(t, chunks...) {
		if content := c.Candidates[0].Content; content != nil {
			streamed = append(streamed, content)
		}
	}
	for name, history := range map[string][]*Content{
		"stream": streamed,
		"JSON":   {toCandidate(accumulated.Result().Message.Content, FinishReasonStop).Content},
	} {
		t.Run(name, func(t *testing.T) {
			messages, err := toMessages(nil, history)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, m := range messages {
				for _, p := range m.Content {
					switch {
					case p.Reasoning != nil:
						got = append(got, p.Reasoning.ID+"="+p.Reasoning.Signature)
					case p.ToolCall != nil:
						got = append(got, p.ToolCall.ID)
					default:
						got = append(got, p.Text)
					}
				}
			}
			if want := []string{"r1=enc1", "answer", "r2=enc2", "r3=enc3", "call"}; !slices.Equal(got, want) {
				t.Fatalf("replayed %v, want %v", got, want)
			}
		})
	}
}

func TestZeroArgumentFunctionCallOnWire(t *testing.T) {
	call := provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "get_time"})
	stream := collectStream(t, assistantChunk(call))
	for _, content := range []*Content{toCandidate([]provider.Content{call}, FinishReasonStop).Content, stream[len(stream)-1].Candidates[0].Content} {
		raw, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		var wire Content
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Parts) != 1 || wire.Parts[0].FunctionCall == nil || wire.Parts[0].FunctionCall.Args == nil || len(wire.Parts[0].FunctionCall.Args) != 0 {
			t.Fatalf("synchronous python-genai skips this zero-argument call: %s", raw)
		}
	}
}

func TestInvalidToolCallsAreNotExecutable(t *testing.T) {
	for _, tc := range []struct {
		name, arguments string
		status          provider.CompletionStatus
		want            FinishReason
	}{
		{"no arguments before token limit", "", provider.CompletionStatusIncomplete, FinishReasonMaxTokens},
		{"truncated JSON", `{"city":`, provider.CompletionStatusIncomplete, FinishReasonMaxTokens},
		{"invalid final arguments", `{"city":`, provider.CompletionStatusCompleted, FinishReasonMalformedFunctionCall},
		{"non-object arguments", "null", provider.CompletionStatusCompleted, FinishReasonMalformedFunctionCall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := assistantChunk(
				provider.TextContent("Checking"),
				provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "first", Arguments: "{}"}),
				provider.ToolCallContent(provider.ToolCall{ID: "c2", Name: "second", Arguments: tc.arguments}),
			)
			terminal := provider.Completion{Status: tc.status, StopReason: provider.StopReasonToolUse}
			acc := provider.CompletionAccumulator{}
			acc.Add(calls)
			acc.Add(terminal)
			result := acc.Result()
			candidate := toCandidate(result.Message.Content, toFinishReason(result))
			stream := collectStream(t, calls, terminal)
			for _, reply := range append(stream, GenerateContentResponse{Candidates: []*Candidate{candidate}}) {
				if len(partsOf(reply)) == 0 {
					t.Fatal("empty chunk discards the turn from python-genai chat history")
				}
				for _, p := range partsOf(reply) {
					if p.FunctionCall != nil {
						t.Fatalf("executable call in failed batch: %+v", p.FunctionCall)
					}
				}
			}
			if candidate.FinishReason != tc.want || stream[len(stream)-1].Candidates[0].FinishReason != tc.want {
				t.Fatalf("incorrect finish reason for %s", tc.name)
			}
		})
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
	if len(parts) != 1 || parts[0].Text != "Answer" || signedBy(parts[0]) != "gemsig_1=SIG" {
		t.Fatalf("expected text part carrying the signature, got %+v", parts)
	}
}

func TestStreamingFunctionCallSignatureSurvivesReplay(t *testing.T) {
	for _, summary := range []string{"", "Checking both cities"} {
		t.Run(summary, func(t *testing.T) {
			out := collectStream(t,
				assistantChunk(provider.TextContent("Let me check.")),
				assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts(summary)})),
				assistantChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Signature: "U0lH"})),
				assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "c1", Name: "get_weather"})),
				assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "c1", Arguments: `{"city":"Bern"}`})),
				assistantChunk(provider.ToolCallContent(provider.ToolCall{ID: "c2", Name: "get_weather", Arguments: `{"city":"Zurich"}`})),
			)
			reply := &Content{Role: "model"}
			signatures := 0
			for _, chunk := range out {
				for _, part := range partsOf(chunk) {
					reply.Parts = append(reply.Parts, part)
					if len(part.ThoughtSignature) > 0 {
						signatures++
					}
				}
			}
			final := partsOf(out[len(out)-1])
			if len(final) != 2 || final[0].FunctionCall == nil || signedBy(final[0]) != "rs_1=U0lH" || len(final[1].ThoughtSignature) != 0 || signatures != 1 {
				t.Fatalf("signature must appear once, on the first call: final=%+v signatures=%d", final, signatures)
			}
			messages, err := toMessages(nil, []*Content{reply})
			if err != nil {
				t.Fatal(err)
			}
			content := messages[0].Content
			for i, c := range content {
				if c.ToolCall != nil && c.ToolCall.ID == "c1" {
					if i == 0 || content[i-1].Reasoning == nil || content[i-1].Reasoning.ID != "rs_1" || content[i-1].Reasoning.Signature != "U0lH" || c.ToolCall.Arguments != `{"city":"Bern"}` {
						t.Fatalf("replayed call lost signature or arguments: %+v", content)
					}
				}
			}
		})
	}
}

// Anthropic emits signed reasoning blocks without item IDs. Both Gemini
// transports must preserve every signature and the answer on either side.
func TestAnonymousReasoningReplayOrder(t *testing.T) {
	chunks := []provider.Completion{
		assistantChunk(provider.TextContent("before")),
		assistantChunk(provider.ReasoningContent(provider.Reasoning{Signature: "@alias:first"})),
		assistantChunk(provider.ReasoningContent(provider.Reasoning{Signature: "@alias:second"})),
		assistantChunk(provider.TextContent("after")),
	}
	var acc provider.CompletionAccumulator
	for _, c := range chunks {
		acc.Add(c)
	}
	var streamed []*Content
	for _, c := range collectStream(t, chunks...) {
		streamed = append(streamed, c.Candidates[0].Content)
	}
	for name, history := range map[string][]*Content{"stream": streamed, "json": {toCandidate(acc.Result().Message.Content, FinishReasonStop).Content}} {
		t.Run(name, func(t *testing.T) {
			messages, err := toMessages(nil, history)
			if err != nil {
				t.Fatal(err)
			}
			var replay []string
			for _, m := range messages {
				for _, c := range m.Content {
					if c.Reasoning != nil {
						replay = append(replay, c.Reasoning.Signature)
					} else {
						replay = append(replay, c.Text)
					}
				}
			}
			if want := []string{"before", "@alias:first", "@alias:second", "after"}; !slices.Equal(replay, want) {
				t.Fatalf("replayed %v, want %v", replay, want)
			}
		})
	}
}
