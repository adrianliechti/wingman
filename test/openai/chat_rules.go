package openai

import "github.com/adrianliechti/wingman/test/harness"

// DefaultChatResponseRules returns comparison rules for /v1/chat/completions.
func DefaultChatResponseRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"id":         harness.FieldPresence,
		"created":    harness.FieldPresence,
		"model":      harness.FieldIgnore,

		"choices.*.message.content":  harness.FieldIgnore,
		"choices.*.message.refusal":  harness.FieldIgnore,

		"usage.prompt_tokens":                                  harness.FieldNonEmpty,
		"usage.completion_tokens":                               harness.FieldNonEmpty,
		"usage.total_tokens":                                    harness.FieldNonEmpty,
		"usage.prompt_tokens_details":                           harness.FieldIgnore,
		"usage.completion_tokens_details":                       harness.FieldIgnore,

		"service_tier":       harness.FieldPresence,
		"system_fingerprint": harness.FieldIgnore,
	}
}

// DefaultChatSSEEventRules returns comparison rules for chat SSE events.
func DefaultChatSSEEventRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"id":         harness.FieldPresence,
		"created":    harness.FieldPresence,
		"model":      harness.FieldIgnore,

		"choices.*.delta.content":   harness.FieldIgnore,
		"choices.*.delta.refusal":   harness.FieldIgnore,

		"service_tier":       harness.FieldPresence,
		"system_fingerprint": harness.FieldIgnore,
		"obfuscation":        harness.FieldIgnore,

		"usage.prompt_tokens":                                  harness.FieldNonEmpty,
		"usage.completion_tokens":                               harness.FieldNonEmpty,
		"usage.total_tokens":                                    harness.FieldNonEmpty,
		"usage.prompt_tokens_details":                           harness.FieldIgnore,
		"usage.completion_tokens_details":                       harness.FieldIgnore,
	}
}
