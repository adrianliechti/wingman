package responses

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestCacheUsageSnapshots(t *testing.T) {
	acc := NewStreamingAccumulator(func(StreamEvent) error { return nil })
	add := func(usage *provider.Usage) {
		t.Helper()
		if err := acc.Add(provider.Completion{Usage: usage}); err != nil {
			t.Fatal(err)
		}
	}
	add(&provider.Usage{CacheCreationInputTokens: 100, CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{ExtendedTokens: 100}})
	add(&provider.Usage{CacheCreationInputTokens: 80, CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{StandardTokens: 80}})
	usage := acc.Result().Usage
	if usage.CacheCreationInputTokens != 100 || usage.CacheWriteTokensDetails.ExtendedTokens != 100 || usage.CacheWriteTokensDetails.StandardTokens != 0 {
		t.Fatalf("inconsistent cache usage: %+v", usage)
	}
	add(&provider.Usage{CacheCreationInputTokens: 120})
	if acc.Result().Usage.CacheWriteTokensDetails != nil {
		t.Fatal("kept stale cache breakdown")
	}
}
