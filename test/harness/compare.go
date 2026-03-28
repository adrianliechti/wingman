package harness

import (
	"fmt"
	"strings"
	"testing"
)

// FieldRule defines how a specific JSON field should be compared.
type FieldRule int

const (
	// FieldExact requires the field values to be identical.
	FieldExact FieldRule = iota
	// FieldPresence requires the field to be present (or absent) in both, but ignores the value.
	FieldPresence
	// FieldIgnore skips comparison of this field entirely.
	FieldIgnore
	// FieldType requires the field to have the same JSON type but not necessarily the same value.
	FieldType
	// FieldNonEmpty requires the field to be present and non-zero/non-empty in both.
	FieldNonEmpty
)

// CompareOption configures structural comparison.
type CompareOption struct {
	// Rules maps dot-separated JSON paths to comparison rules.
	// e.g. "id" -> FieldPresence, "output.0.id" -> FieldPresence
	// Use "*" as a wildcard for array indices: "output.*.id" -> FieldPresence
	Rules map[string]FieldRule
}

// DefaultResponseRules returns comparison rules suitable for /v1/responses.
func DefaultResponseRules() map[string]FieldRule {
	return map[string]FieldRule{
		"id":                        FieldPresence,
		"created_at":                FieldPresence,
		"completed_at":              FieldPresence,
		"model":                     FieldIgnore,
		"output.*.id":               FieldPresence,
		"output.*.content.*.text":   FieldIgnore,
		"usage.input_tokens":        FieldNonEmpty,
		"usage.output_tokens":       FieldNonEmpty,
		"usage.total_tokens":        FieldNonEmpty,
		"error":                     FieldExact,

		// OpenAI includes billing; wingman may omit it
		"billing": FieldIgnore,
		// OpenAI defaults store=true; wingman uses false as safe default
		"store": FieldIgnore,
		// OpenAI returns the resolved model name; wingman echoes the request model
		"service_tier": FieldPresence,
	}
}

// DefaultSSEEventRules returns comparison rules for SSE event structures.
func DefaultSSEEventRules() map[string]FieldRule {
	return map[string]FieldRule{
		"response.id":                              FieldPresence,
		"response.created_at":                      FieldPresence,
		"response.completed_at":                    FieldPresence,
		"response.model":                           FieldIgnore,
		"response.output.*.id":                     FieldPresence,
		"response.output.*.content.*.text":         FieldIgnore,
		"response.usage.input_tokens":              FieldNonEmpty,
		"response.usage.output_tokens":             FieldNonEmpty,
		"response.usage.total_tokens":              FieldNonEmpty,
		"response.billing":                         FieldIgnore,
		"response.store":                           FieldIgnore,
		"response.service_tier":                    FieldPresence,
		"item_id":                                  FieldPresence,
		"item.id":                                  FieldPresence,
		"item.content.*.text":                      FieldIgnore,
		"delta":                                    FieldIgnore,
		"text":                                     FieldIgnore,
		"sequence_number":                          FieldIgnore,
		"part.text":                                FieldIgnore,
		"obfuscation":                              FieldIgnore,
	}
}

// CompareStructure compares two JSON objects structurally.
// It checks that the same fields are set/unset in both, applying rules for specific paths.
// Returns a list of differences found.
func CompareStructure(t *testing.T, label string, expected, actual map[string]any, opts CompareOption) []string {
	t.Helper()
	var diffs []string
	compareMap(t, &diffs, "", expected, actual, opts)

	for _, d := range diffs {
		t.Errorf("[%s] %s", label, d)
	}

	return diffs
}

func compareMap(t *testing.T, diffs *[]string, prefix string, expected, actual map[string]any, opts CompareOption) {
	t.Helper()

	// Check all keys in expected exist in actual
	for key, ev := range expected {
		path := joinPath(prefix, key)
		av, ok := actual[key]

		rule := resolveRule(path, opts.Rules)

		if rule == FieldIgnore {
			continue
		}

		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("field %q present in expected but missing in actual", path))
			continue
		}

		switch rule {
		case FieldPresence:
			// Both present — good enough
			continue
		case FieldNonEmpty:
			if isEmpty(ev) {
				*diffs = append(*diffs, fmt.Sprintf("field %q is empty in expected", path))
			}
			if isEmpty(av) {
				*diffs = append(*diffs, fmt.Sprintf("field %q is empty in actual", path))
			}
		case FieldType:
			if jsonType(ev) != jsonType(av) {
				*diffs = append(*diffs, fmt.Sprintf("field %q type mismatch: expected %s, actual %s", path, jsonType(ev), jsonType(av)))
			}
		default: // FieldExact or unspecified
			compareValues(t, diffs, path, ev, av, opts)
		}
	}

	// Check for extra keys in actual
	for key := range actual {
		path := joinPath(prefix, key)
		rule := resolveRule(path, opts.Rules)
		if rule == FieldIgnore {
			continue
		}

		if _, ok := expected[key]; !ok {
			*diffs = append(*diffs, fmt.Sprintf("field %q present in actual but missing in expected", path))
		}
	}
}

func compareValues(t *testing.T, diffs *[]string, path string, expected, actual any, opts CompareOption) {
	t.Helper()

	switch ev := expected.(type) {
	case map[string]any:
		av, ok := actual.(map[string]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("field %q: expected object, got %s", path, jsonType(actual)))
			return
		}
		compareMap(t, diffs, path, ev, av, opts)

	case []any:
		av, ok := actual.([]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("field %q: expected array, got %s", path, jsonType(actual)))
			return
		}
		if len(ev) != len(av) {
			*diffs = append(*diffs, fmt.Sprintf("field %q: array length mismatch: expected %d, actual %d", path, len(ev), len(av)))
			return
		}
		for i := range ev {
			elemPath := fmt.Sprintf("%s.%d", path, i)
			compareValues(t, diffs, elemPath, ev[i], av[i], opts)
		}

	default:
		if fmt.Sprintf("%v", expected) != fmt.Sprintf("%v", actual) {
			*diffs = append(*diffs, fmt.Sprintf("field %q: value mismatch: expected %v, actual %v", path, expected, actual))
		}
	}
}

// resolveRule looks up the rule for a path, supporting wildcard "*" for array indices.
func resolveRule(path string, rules map[string]FieldRule) FieldRule {
	if rule, ok := rules[path]; ok {
		return rule
	}

	// Try wildcard patterns: replace numeric segments with *
	parts := strings.Split(path, ".")
	for i, p := range parts {
		if isNumeric(p) {
			parts[i] = "*"
		}
	}
	wildcard := strings.Join(parts, ".")
	if rule, ok := rules[wildcard]; ok {
		return rule
	}

	// No rule found — default to exact comparison for leaf values
	return FieldExact
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case float64:
		return val == 0
	case bool:
		return !val
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	}
	return false
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("unknown(%T)", v)
	}
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
