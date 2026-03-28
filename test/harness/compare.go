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
		default:
			compareValues(t, diffs, path, ev, av, opts)
		}
	}

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

func resolveRule(path string, rules map[string]FieldRule) FieldRule {
	if rule, ok := rules[path]; ok {
		return rule
	}

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
