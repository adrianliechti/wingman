package gemini

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

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
		ReasoningTokens: 6,
	})

	if metadata == nil {
		t.Fatal("expected usage metadata")
	}
	if metadata.CandidatesTokenCount != 14 {
		t.Fatalf("expected candidates token count 14, got %d", metadata.CandidatesTokenCount)
	}
	if metadata.ThoughtsTokenCount != 6 {
		t.Fatalf("expected thoughts token count 6, got %d", metadata.ThoughtsTokenCount)
	}
	if metadata.TotalTokenCount != 120 {
		t.Fatalf("expected total token count 120, got %d", metadata.TotalTokenCount)
	}
}
