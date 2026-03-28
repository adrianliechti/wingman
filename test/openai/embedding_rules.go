package openai

import "github.com/adrianliechti/wingman/test/harness"

// DefaultEmbeddingResponseRules returns comparison rules for /v1/embeddings.
func DefaultEmbeddingResponseRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"model":              harness.FieldIgnore,
		"data.*.embedding":   harness.FieldPresence,
		"usage.prompt_tokens": harness.FieldNonEmpty,
		"usage.total_tokens":  harness.FieldNonEmpty,
	}
}
