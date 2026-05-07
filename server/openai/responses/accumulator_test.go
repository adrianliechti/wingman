package responses

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestStreamingAccumulatorMergesUsage(t *testing.T) {
	acc := NewStreamingAccumulator(func(StreamEvent) error {
		return nil
	})

	err := acc.Add(provider.Completion{
		Usage: &provider.Usage{
			InputTokens:              100,
			CacheReadInputTokens:     80,
			CacheCreationInputTokens: 20,
		},
	})
	if err != nil {
		t.Fatalf("add first usage: %v", err)
	}

	err = acc.Add(provider.Completion{
		Usage: &provider.Usage{
			OutputTokens:         30,
			CacheReadInputTokens: 70,
		},
	})
	if err != nil {
		t.Fatalf("add second usage: %v", err)
	}

	usage := acc.Result().Usage
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.InputTokens != 100 {
		t.Fatalf("expected input tokens 100, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 30 {
		t.Fatalf("expected output tokens 30, got %d", usage.OutputTokens)
	}
	if usage.CacheReadInputTokens != 80 {
		t.Fatalf("expected cache read input tokens 80, got %d", usage.CacheReadInputTokens)
	}
	if usage.CacheCreationInputTokens != 20 {
		t.Fatalf("expected cache creation input tokens 20, got %d", usage.CacheCreationInputTokens)
	}
}

// When two reasoning items stream through the accumulator (different IDs),
// each must produce its own reasoning_item.done event with its own
// encrypted_content — and Result() must report them as two separate
// Reasoning contents. Otherwise OpenAI rejects the next turn with
// "encrypted content for item rs_xxx could not be verified", because
// rs_1's ID would carry rs_2's encrypted_content.
func TestStreamingAccumulatorPreservesDistinctReasoningItems(t *testing.T) {
	type doneEvent struct {
		ID        string
		Text      string
		Summary   string
		Signature string
	}

	var doneEvents []doneEvent

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		if e.Type == StreamEventReasoningItemDone {
			doneEvents = append(doneEvents, doneEvent{
				ID:        e.ReasoningID,
				Text:      e.ReasoningText,
				Summary:   e.ReasoningSummary,
				Signature: e.ReasoningSignature,
			})
		}
		return nil
	})

	addReasoning := func(r provider.Reasoning) {
		t.Helper()
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{Content: []provider.Content{provider.ReasoningContent(r)}},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	// Item 1
	addReasoning(provider.Reasoning{ID: "rs_1", Text: "thinking-1"})
	addReasoning(provider.Reasoning{ID: "rs_1", Summary: "summary-1"})
	addReasoning(provider.Reasoning{ID: "rs_1", Signature: "ENC_1"})

	// Item 2 (different ID) — should close item 1 first
	addReasoning(provider.Reasoning{ID: "rs_2", Text: "thinking-2"})
	addReasoning(provider.Reasoning{ID: "rs_2", Summary: "summary-2"})
	addReasoning(provider.Reasoning{ID: "rs_2", Signature: "ENC_2"})

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if len(doneEvents) != 2 {
		t.Fatalf("expected 2 reasoning_item.done events, got %d: %+v", len(doneEvents), doneEvents)
	}
	if doneEvents[0].ID != "rs_1" || doneEvents[0].Signature != "ENC_1" {
		t.Errorf("done event 0: expected (rs_1, ENC_1), got (%s, %s)", doneEvents[0].ID, doneEvents[0].Signature)
	}
	if doneEvents[1].ID != "rs_2" || doneEvents[1].Signature != "ENC_2" {
		t.Errorf("done event 1: expected (rs_2, ENC_2), got (%s, %s)", doneEvents[1].ID, doneEvents[1].Signature)
	}

	result := acc.Result()
	var reasonings []provider.Reasoning
	for _, c := range result.Message.Content {
		if c.Reasoning != nil {
			reasonings = append(reasonings, *c.Reasoning)
		}
	}
	if len(reasonings) != 2 {
		t.Fatalf("expected 2 reasoning contents in Result(), got %d", len(reasonings))
	}
	if reasonings[0].ID != "rs_1" || reasonings[0].Signature != "ENC_1" {
		t.Errorf("result item 0: expected (rs_1, ENC_1), got (%s, %s)", reasonings[0].ID, reasonings[0].Signature)
	}
	if reasonings[1].ID != "rs_2" || reasonings[1].Signature != "ENC_2" {
		t.Errorf("result item 1: expected (rs_2, ENC_2), got (%s, %s)", reasonings[1].ID, reasonings[1].Signature)
	}
}

func TestStreamingAccumulatorPreservesCompactionOrder(t *testing.T) {
	var events []StreamEvent

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})

	add := func(contents ...provider.Content) {
		t.Helper()
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{Content: contents},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	add(provider.CompactionContent(provider.Compaction{ID: "cmp_1", Signature: "ENC_1"}))
	add(provider.TextContent("4"))
	add(provider.CompactionContent(provider.Compaction{ID: "cmp_2", Signature: "ENC_2"}))

	if err := acc.Complete(); err != nil {
		t.Fatalf("complete: %v", err)
	}

	var compactionEvents []StreamEvent
	for _, event := range events {
		if event.Type == StreamEventCompactionItemDone {
			compactionEvents = append(compactionEvents, event)
		}
	}

	if len(compactionEvents) != 2 {
		t.Fatalf("expected 2 compaction done events, got %d: %+v", len(compactionEvents), compactionEvents)
	}
	if compactionEvents[0].CompactionID != "cmp_1" || compactionEvents[0].CompactionContent != "ENC_1" {
		t.Fatalf("event 0: expected cmp_1, got %+v", compactionEvents[0])
	}
	if compactionEvents[1].CompactionID != "cmp_2" || compactionEvents[1].CompactionContent != "ENC_2" {
		t.Fatalf("event 1: expected cmp_2, got %+v", compactionEvents[1])
	}

	contents := acc.Result().Message.Content
	if len(contents) != 3 {
		t.Fatalf("expected 3 content items, got %d: %+v", len(contents), contents)
	}
	if contents[0].Compaction == nil || contents[0].Compaction.ID != "cmp_1" {
		t.Fatalf("content 0: expected cmp_1, got %+v", contents[0])
	}
	if contents[1].Text != "4" {
		t.Fatalf("content 1: expected text, got %+v", contents[1])
	}
	if contents[2].Compaction == nil || contents[2].Compaction.ID != "cmp_2" {
		t.Fatalf("content 2: expected cmp_2, got %+v", contents[2])
	}
}
