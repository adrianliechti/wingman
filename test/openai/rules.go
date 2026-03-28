package openaitest

import "github.com/adrianliechti/wingman/test/harness"

// DefaultResponseRules returns comparison rules suitable for /v1/responses.
func DefaultResponseRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"id":                        harness.FieldPresence,
		"created_at":                harness.FieldPresence,
		"completed_at":              harness.FieldPresence,
		"model":                     harness.FieldIgnore,
		"output.*.id":               harness.FieldPresence,
		"output.*.content.*.text":   harness.FieldIgnore,
		"output.*.encrypted_content": harness.FieldNonEmpty,
		"output.*.summary":          harness.FieldPresence,
		"output.*.summary.*.text":   harness.FieldIgnore,

		"usage.input_tokens":                           harness.FieldNonEmpty,
		"usage.output_tokens":                          harness.FieldNonEmpty,
		"usage.total_tokens":                           harness.FieldNonEmpty,
		"usage.input_tokens_details.cached_tokens":     harness.FieldPresence,
		"usage.output_tokens_details.reasoning_tokens": harness.FieldPresence,
		"error":                                        harness.FieldExact,

		"billing":           harness.FieldIgnore,
		"store":             harness.FieldIgnore,
		"reasoning.summary": harness.FieldIgnore,
		"service_tier":      harness.FieldPresence,
	}
}

// DefaultSSEEventRules returns comparison rules for SSE event structures.
func DefaultSSEEventRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"response.id":                                           harness.FieldPresence,
		"response.created_at":                                   harness.FieldPresence,
		"response.completed_at":                                 harness.FieldPresence,
		"response.model":                                        harness.FieldIgnore,
		"response.output.*.id":                                  harness.FieldPresence,
		"response.output.*.content.*.text":                      harness.FieldIgnore,
		"response.output.*.summary":                             harness.FieldPresence,
		"response.output.*.summary.*.text":                      harness.FieldIgnore,
		"response.output.*.encrypted_content":                   harness.FieldNonEmpty,
		"response.usage.input_tokens":                           harness.FieldNonEmpty,
		"response.usage.output_tokens":                          harness.FieldNonEmpty,
		"response.usage.total_tokens":                           harness.FieldNonEmpty,
		"response.usage.input_tokens_details.cached_tokens":     harness.FieldPresence,
		"response.usage.output_tokens_details.reasoning_tokens": harness.FieldPresence,
		"response.reasoning.summary":                            harness.FieldIgnore,
		"response.billing":                                      harness.FieldIgnore,
		"response.store":                                        harness.FieldIgnore,
		"response.service_tier":                                 harness.FieldPresence,

		"item_id":             harness.FieldPresence,
		"item.id":             harness.FieldPresence,
		"item.encrypted_content": harness.FieldNonEmpty,
		"item.content.*.text": harness.FieldIgnore,
		"item.summary":        harness.FieldIgnore,
		"item.summary.*.text": harness.FieldIgnore,
		"item.status":         harness.FieldIgnore,
		"item.content":        harness.FieldIgnore,

		"delta":          harness.FieldIgnore,
		"text":           harness.FieldIgnore,
		"sequence_number": harness.FieldIgnore,
		"part.text":      harness.FieldIgnore,
		"obfuscation":    harness.FieldIgnore,
		"summary_index":  harness.FieldIgnore,
	}
}
