package anthropic

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSanitizeStrictSchema exercises the sanitizer directly against the
// documented strict-mode limitations: constraint keywords are stripped and
// folded into descriptions, supported keywords survive, schema-position
// awareness protects properties that share a keyword name, and the input is
// never mutated.
func TestSanitizeStrictSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"startLine": map[string]any{
				"type":        []any{"integer", "null"},
				"minimum":     float64(1),
				"description": "Start line",
			},
			"path": map[string]any{
				"type":   "string",
				"format": "path", // unsupported format → stripped
			},
			"website": map[string]any{
				"type":   "string",
				"format": "uri", // supported format → kept
			},
			"code": map[string]any{
				"type":    "string",
				"pattern": "^[a-z]+$", // supported → kept
			},
			"tags": map[string]any{
				"type":     "array",
				"items":    map[string]any{"type": "string", "maxLength": float64(20)},
				"minItems": float64(1), // supported value → kept
				"maxItems": float64(5), // unsupported → stripped
			},
			"minimum": map[string]any{"type": "number"}, // property NAMED minimum → kept
			"choice": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "integer", "maximum": float64(10)},
					map[string]any{"type": "null"},
				},
			},
		},
		"$defs": map[string]any{
			"page": map[string]any{"type": "integer", "multipleOf": float64(2)},
		},
		"required":             []any{"startLine", "path"},
		"additionalProperties": false,
	}

	before, _ := json.Marshal(schema)

	result := sanitizeStrictSchema(schema)

	after, _ := json.Marshal(schema)
	if string(before) != string(after) {
		t.Fatal("input schema was mutated")
	}

	props := result["properties"].(map[string]any)

	start := props["startLine"].(map[string]any)
	if _, ok := start["minimum"]; ok {
		t.Error("minimum not stripped")
	}
	if desc, _ := start["description"].(string); !strings.Contains(desc, "minimum 1") {
		t.Errorf("stripped constraint not folded into description: %q", desc)
	}

	if _, ok := props["path"].(map[string]any)["format"]; ok {
		t.Error("unsupported format not stripped")
	}
	if props["website"].(map[string]any)["format"] != "uri" {
		t.Error("supported format wrongly stripped")
	}
	if props["code"].(map[string]any)["pattern"] != "^[a-z]+$" {
		t.Error("pattern wrongly stripped")
	}

	tags := props["tags"].(map[string]any)
	if tags["minItems"] != float64(1) {
		t.Error("minItems 1 wrongly stripped")
	}
	if _, ok := tags["maxItems"]; ok {
		t.Error("maxItems not stripped")
	}
	if _, ok := tags["items"].(map[string]any)["maxLength"]; ok {
		t.Error("maxLength not stripped from items sub-schema")
	}

	if _, ok := props["minimum"]; !ok {
		t.Error("property named 'minimum' wrongly stripped")
	}

	branch := props["choice"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	if _, ok := branch["maximum"]; ok {
		t.Error("maximum not stripped inside anyOf branch")
	}

	page := result["$defs"].(map[string]any)["page"].(map[string]any)
	if _, ok := page["multipleOf"]; ok {
		t.Error("multipleOf not stripped inside $defs")
	}

	if result["additionalProperties"] != false {
		t.Error("additionalProperties lost")
	}
}
