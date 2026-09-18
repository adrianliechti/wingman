package gemini

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestUsageMetadataPreservesReasoningPresence(t *testing.T) {
	for _, count := range []*int{nil, new(0), new(3)} {
		metadata := toUsageMetadata(&provider.Usage{OutputTokens: 5, ReasoningTokens: count})
		data, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		value, reported := wire["thoughtsTokenCount"]
		if reported != (count != nil) || reported && value != float64(*count) {
			t.Fatalf("wire usage = %s, want reasoning count %v", data, count)
		}
	}
}

func TestUsageMetadataIncludesCachedTokens(t *testing.T) {
	metadata := toUsageMetadata(&provider.Usage{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 80,
	})

	if metadata == nil {
		t.Fatal("expected usage metadata")
	}
	if metadata.CachedContentTokenCount != 80 {
		t.Fatalf("expected cached content token count 80, got %d", metadata.CachedContentTokenCount)
	}
	if metadata.TotalTokenCount != 120 {
		t.Fatalf("expected total token count 120, got %d", metadata.TotalTokenCount)
	}
}

// TestUsageMetadataSplitsReasoningFromCandidates verifies that the intermediate
// reasoning-inclusive OutputTokens is split back into Gemini's wire shape, where
// CandidatesTokenCount excludes thinking and ThoughtsTokenCount carries it.
func TestUsageMetadataSplitsReasoningFromCandidates(t *testing.T) {
	metadata := toUsageMetadata(&provider.Usage{
		InputTokens:     100,
		OutputTokens:    20, // 14 visible + 6 thinking
		ReasoningTokens: new(6),
	})

	if metadata == nil {
		t.Fatal("expected usage metadata")
	}
	if metadata.CandidatesTokenCount != 14 {
		t.Fatalf("expected candidates token count 14, got %d", metadata.CandidatesTokenCount)
	}
	if metadata.ThoughtsTokenCount == nil {
		t.Fatal("expected thoughts token count")
	}
	if *metadata.ThoughtsTokenCount != 6 {
		t.Fatalf("expected thoughts token count 6, got %d", *metadata.ThoughtsTokenCount)
	}
	if metadata.TotalTokenCount != 120 {
		t.Fatalf("expected total token count 120, got %d", metadata.TotalTokenCount)
	}
}

func TestNormalizeResponseSchema(t *testing.T) {
	schema := map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"title":  map[string]any{"type": "STRING"},
			"series": map[string]any{"type": "STRING", "nullable": true},
			"tags":   map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
		},
		"required":         []string{"title"},
		"propertyOrdering": []string{"title", "series", "tags"},
	}

	got := normalizeResponseSchema(schema)

	if got["type"] != "object" {
		t.Errorf("type: %v", got["type"])
	}

	if _, ok := got["propertyOrdering"]; ok {
		t.Error("propertyOrdering should be stripped")
	}

	props := got["properties"].(map[string]any)

	if props["title"].(map[string]any)["type"] != "string" {
		t.Errorf("title type: %v", props["title"])
	}

	series := props["series"].(map[string]any)
	if _, ok := series["nullable"]; ok {
		t.Error("nullable should be folded into type")
	}
	if types, _ := series["type"].([]any); len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("series type: %v", series["type"])
	}

	items := props["tags"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("items type: %v", items["type"])
	}

	// input untouched
	if schema["type"] != "OBJECT" {
		t.Error("input schema was mutated")
	}
}
