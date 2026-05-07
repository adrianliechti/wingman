package provider

import (
	"strings"
	"testing"
)

func TestCompletionAccumulatorPreservesCacheUsage(t *testing.T) {
	acc := CompletionAccumulator{}

	acc.Add(Completion{
		Usage: &Usage{
			InputTokens:              12,
			OutputTokens:             3,
			CacheReadInputTokens:     40,
			CacheCreationInputTokens: 50,
		},
	})
	acc.Add(Completion{
		Usage: &Usage{
			InputTokens:              10,
			OutputTokens:             5,
			CacheReadInputTokens:     35,
			CacheCreationInputTokens: 45,
		},
	})

	usage := acc.Result().Usage
	if usage == nil {
		t.Fatal("expected usage")
	}

	if usage.InputTokens != 12 {
		t.Fatalf("expected input tokens 12, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 5 {
		t.Fatalf("expected output tokens 5, got %d", usage.OutputTokens)
	}
	if usage.CacheReadInputTokens != 40 {
		t.Fatalf("expected cache read input tokens 40, got %d", usage.CacheReadInputTokens)
	}
	if usage.CacheCreationInputTokens != 50 {
		t.Fatalf("expected cache creation input tokens 50, got %d", usage.CacheCreationInputTokens)
	}
}

// When a streaming response carries two reasoning items, each must end up
// as its own Reasoning entry. Collapsing them pairs item 1's ID with
// item 2's encrypted_content, which OpenAI rejects on the next turn with
// "encrypted content for item rs_xxx could not be verified".
func TestCompletionAccumulatorPreservesDistinctReasoningItems(t *testing.T) {
	acc := CompletionAccumulator{}

	// Item 1 — interleaved deltas with the same ID, then signature.
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_1", Text: "thinking "})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_1", Summary: "sum1 "})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_1", Text: "step a"})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_1", Signature: "ENC_1"})}}})

	// Item 2 — same delta pattern, different ID.
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_2", Text: "more "})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_2", Summary: "sum2"})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{ID: "rs_2", Signature: "ENC_2"})}}})

	result := acc.Result()
	if result.Message == nil {
		t.Fatal("expected message")
	}

	var reasonings []Reasoning
	for _, c := range result.Message.Content {
		if c.Reasoning != nil {
			reasonings = append(reasonings, *c.Reasoning)
		}
	}

	if len(reasonings) != 2 {
		t.Fatalf("expected 2 reasoning items, got %d: %+v", len(reasonings), reasonings)
	}

	if reasonings[0].ID != "rs_1" || reasonings[0].Signature != "ENC_1" {
		t.Errorf("item 0: expected (rs_1, ENC_1), got (%s, %s)", reasonings[0].ID, reasonings[0].Signature)
	}
	if reasonings[1].ID != "rs_2" || reasonings[1].Signature != "ENC_2" {
		t.Errorf("item 1: expected (rs_2, ENC_2), got (%s, %s)", reasonings[1].ID, reasonings[1].Signature)
	}

	if !strings.Contains(reasonings[0].Text, "thinking") || !strings.Contains(reasonings[0].Text, "step a") {
		t.Errorf("item 0 text wrong: %q", reasonings[0].Text)
	}
	if strings.Contains(reasonings[0].Text, "more") {
		t.Errorf("item 0 text leaked from item 1: %q", reasonings[0].Text)
	}
	if reasonings[0].Summary != "sum1 " {
		t.Errorf("item 0 summary wrong: %q", reasonings[0].Summary)
	}
	if reasonings[1].Summary != "sum2" {
		t.Errorf("item 1 summary wrong: %q", reasonings[1].Summary)
	}
}

// Anthropic-style reasoning chunks have no item ID; they should still merge
// into a single Reasoning entry as before.
func TestCompletionAccumulatorMergesIDLessReasoning(t *testing.T) {
	acc := CompletionAccumulator{}

	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{Text: "hello "})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{Text: "world"})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{ReasoningContent(Reasoning{Signature: "SIG"})}}})

	result := acc.Result()

	var reasonings []Reasoning
	for _, c := range result.Message.Content {
		if c.Reasoning != nil {
			reasonings = append(reasonings, *c.Reasoning)
		}
	}

	if len(reasonings) != 1 {
		t.Fatalf("expected 1 reasoning, got %d", len(reasonings))
	}
	if reasonings[0].Text != "hello world" {
		t.Errorf("text: got %q, want %q", reasonings[0].Text, "hello world")
	}
	if reasonings[0].Signature != "SIG" {
		t.Errorf("signature: got %q, want SIG", reasonings[0].Signature)
	}
}

func TestCompletionAccumulatorPreservesCompactionOrder(t *testing.T) {
	acc := CompletionAccumulator{}

	acc.Add(Completion{Message: &Message{Content: []Content{CompactionContent(Compaction{ID: "cmp_1", Signature: "ENC_1"})}}})
	acc.Add(Completion{Message: &Message{Content: []Content{TextContent("answer")}}})
	acc.Add(Completion{Message: &Message{Content: []Content{CompactionContent(Compaction{ID: "cmp_2", Signature: "ENC_2"})}}})

	result := acc.Result()
	if result.Message == nil {
		t.Fatal("expected message")
	}

	contents := result.Message.Content
	if len(contents) != 3 {
		t.Fatalf("expected 3 content items, got %d: %+v", len(contents), contents)
	}

	if contents[0].Compaction == nil || contents[0].Compaction.ID != "cmp_1" || contents[0].Compaction.Signature != "ENC_1" {
		t.Fatalf("content 0: expected cmp_1, got %+v", contents[0])
	}
	if contents[1].Text != "answer" {
		t.Fatalf("content 1: expected text answer, got %+v", contents[1])
	}
	if contents[2].Compaction == nil || contents[2].Compaction.ID != "cmp_2" || contents[2].Compaction.Signature != "ENC_2" {
		t.Fatalf("content 2: expected cmp_2, got %+v", contents[2])
	}
}
