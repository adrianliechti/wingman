package gemini

import "github.com/adrianliechti/wingman/test/harness"

// DefaultResponseRules returns comparison rules for generateContent.
func DefaultResponseRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"responseId":   harness.FieldPresence,
		"modelVersion": harness.FieldIgnore,

		"candidates.*.content.parts.*.text":             harness.FieldIgnore,
		"candidates.*.content.parts.*.thoughtSignature": harness.FieldIgnore,
		"candidates.*.index":                            harness.FieldIgnore,
		"candidates.*.finishReason":                     harness.FieldIgnore,
		"candidates.*.finishMessage":                    harness.FieldIgnore,
		"candidates.*.safetyRatings":                    harness.FieldIgnore,
		"candidates.*.tokenCount":                       harness.FieldIgnore,

		"usageMetadata.promptTokenCount":      harness.FieldNonEmpty,
		"usageMetadata.candidatesTokenCount":  harness.FieldIgnore,
		"usageMetadata.totalTokenCount":       harness.FieldNonEmpty,
		"usageMetadata.promptTokensDetails":   harness.FieldIgnore,
		"usageMetadata.thoughtsTokenCount":    harness.FieldIgnore,

		"promptFeedback": harness.FieldIgnore,
	}
}

// DefaultSSERules returns comparison rules for streamGenerateContent SSE events.
func DefaultSSERules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"responseId":   harness.FieldPresence,
		"modelVersion": harness.FieldIgnore,

		"candidates.*.content.parts.*.text":             harness.FieldIgnore,
		"candidates.*.content.parts.*.thoughtSignature": harness.FieldIgnore,
		"candidates.*.index":                            harness.FieldIgnore,
		"candidates.*.finishReason":                     harness.FieldIgnore,
		"candidates.*.finishMessage":                    harness.FieldIgnore,
		"candidates.*.safetyRatings":                    harness.FieldIgnore,
		"candidates.*.tokenCount":                       harness.FieldIgnore,

		"usageMetadata.promptTokenCount":      harness.FieldIgnore,
		"usageMetadata.candidatesTokenCount":  harness.FieldIgnore,
		"usageMetadata.totalTokenCount":       harness.FieldIgnore,
		"usageMetadata.promptTokensDetails":   harness.FieldIgnore,
		"usageMetadata.thoughtsTokenCount":    harness.FieldIgnore,

		"promptFeedback": harness.FieldIgnore,
	}
}
