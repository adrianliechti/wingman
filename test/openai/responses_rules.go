package openai

import "github.com/adrianliechti/wingman/test/harness"

// DefaultResponsesResponseRules returns comparison rules suitable for /v1/responses.
func DefaultResponsesResponseRules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		// Wingman deliberately omits the removed sampling controls.
		"temperature":                harness.FieldIgnore,
		"top_p":                      harness.FieldIgnore,
		"top_k":                      harness.FieldIgnore,
		"id":                         harness.FieldPresence,
		"created_at":                 harness.FieldPresence,
		"completed_at":               harness.FieldPresence,
		"model":                      harness.FieldIgnore,
		"output.*.id":                harness.FieldPresence,
		"output.*.content":           harness.FieldIgnore,
		"output.*.encrypted_content": harness.FieldIgnore,
		"output.*.summary":           harness.FieldPresence,
		"output.*.summary.*.text":    harness.FieldIgnore,
		"output.*.call_id":           harness.FieldPresence,
		"output.*.arguments":         harness.FieldPresence,
		"output.*.phase":             harness.FieldPresence,
		"output.*.status":            harness.FieldOptional,

		"usage.input_tokens":  harness.FieldNonEmpty,
		"usage.output_tokens": harness.FieldNonEmpty,
		// Some backends do not report a reasoning-token breakdown.
		"usage.output_tokens_details":              harness.FieldOptional,
		"usage.total_tokens":                       harness.FieldNonEmpty,
		"usage.input_tokens_details.cached_tokens": harness.FieldPresence,
		// Cache creation varies by backing provider and request history.
		"usage.input_tokens_details.cache_write_tokens":                        harness.FieldIgnore,
		"usage.input_tokens_details.cache_write_tokens_details":                harness.FieldOptional,
		"usage.input_tokens_details.cache_write_tokens_details.ttl_30m_tokens": harness.FieldType,
		"usage.input_tokens_details.cache_write_tokens_details.ttl_12h_tokens": harness.FieldType,
		"usage.output_tokens_details.reasoning_tokens":                         harness.FieldPresence,
		"error":                   harness.FieldExact,
		"text.format.description": harness.FieldIgnore,
		"prompt_cache_retention":  harness.FieldIgnore,
		// Native OpenAI also reports its image context-compression setting.
		"image": harness.FieldOptional,

		"billing":           harness.FieldIgnore,
		"store":             harness.FieldIgnore,
		"reasoning.summary": harness.FieldIgnore,
		"reasoning.effort":  harness.FieldPresence,
		"reasoning.context": harness.FieldPresence,
		"service_tier":      harness.FieldPresence,
	}
}

// DefaultResponsesSSERules returns comparison rules for SSE event structures.
func DefaultResponsesSSERules() map[string]harness.FieldRule {
	return map[string]harness.FieldRule{
		"response.temperature": harness.FieldIgnore,
		"response.top_p":       harness.FieldIgnore,
		"response.top_k":       harness.FieldIgnore,
		"response.usage.input_tokens_details.cache_write_tokens":                        harness.FieldIgnore,
		"response.usage.input_tokens_details.cache_write_tokens_details":                harness.FieldOptional,
		"response.usage.input_tokens_details.cache_write_tokens_details.ttl_30m_tokens": harness.FieldType,
		"response.usage.input_tokens_details.cache_write_tokens_details.ttl_12h_tokens": harness.FieldType,
		"response.id":                                           harness.FieldPresence,
		"response.created_at":                                   harness.FieldPresence,
		"response.completed_at":                                 harness.FieldPresence,
		"response.model":                                        harness.FieldIgnore,
		"response.output.*.id":                                  harness.FieldPresence,
		"response.output.*.content.*.text":                      harness.FieldIgnore,
		"response.output.*.summary":                             harness.FieldPresence,
		"response.output.*.summary.*.text":                      harness.FieldIgnore,
		"response.output.*.encrypted_content":                   harness.FieldIgnore,
		"response.output.*.call_id":                             harness.FieldPresence,
		"response.output.*.arguments":                           harness.FieldPresence,
		"response.output.*.phase":                               harness.FieldPresence,
		"response.output.*.status":                              harness.FieldOptional,
		"response.usage.input_tokens":                           harness.FieldNonEmpty,
		"response.usage.output_tokens":                          harness.FieldNonEmpty,
		"response.usage.output_tokens_details":                  harness.FieldOptional,
		"response.usage.total_tokens":                           harness.FieldNonEmpty,
		"response.usage.input_tokens_details.cached_tokens":     harness.FieldPresence,
		"response.usage.output_tokens_details.reasoning_tokens": harness.FieldPresence,
		"response.reasoning.effort":                             harness.FieldPresence,
		"response.reasoning.summary":                            harness.FieldIgnore,
		"response.reasoning.context":                            harness.FieldPresence,
		"response.text.format.description":                      harness.FieldIgnore,
		"response.prompt_cache_retention":                       harness.FieldIgnore,
		"response.image":                                        harness.FieldOptional,
		"response.billing":                                      harness.FieldIgnore,
		"response.store":                                        harness.FieldIgnore,
		"response.service_tier":                                 harness.FieldPresence,

		"item_id":                harness.FieldPresence,
		"item.id":                harness.FieldPresence,
		"item.encrypted_content": harness.FieldIgnore,
		"item.call_id":           harness.FieldPresence,
		"item.arguments":         harness.FieldIgnore,
		"item.content.*.text":    harness.FieldIgnore,
		"item.summary":           harness.FieldIgnore,
		"item.summary.*.text":    harness.FieldIgnore,
		"item.status":            harness.FieldIgnore,
		"item.phase":             harness.FieldPresence,
		"item.content":           harness.FieldIgnore,

		"arguments":       harness.FieldIgnore,
		"name":            harness.FieldIgnore,
		"delta":           harness.FieldIgnore,
		"text":            harness.FieldIgnore,
		"sequence_number": harness.FieldIgnore,
		"part.text":       harness.FieldIgnore,
		"obfuscation":     harness.FieldIgnore,
		"summary_index":   harness.FieldIgnore,
	}
}
