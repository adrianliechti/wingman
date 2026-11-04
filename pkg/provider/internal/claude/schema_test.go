package claude

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"

	"github.com/google/jsonschema-go/jsonschema"
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

	before, _ := json.Marshal(schema, json.Deterministic(true))

	result := SanitizeSchema(schema)

	after, _ := json.Marshal(schema, json.Deterministic(true))
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

// TestSanitizeStrictSchemaNullableEnum covers OpenAI's null-widened optional
// enums, which Anthropic's strict validator rejects as a type list + enum.
func TestSanitizeStrictSchemaNullableEnum(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"output_mode": map[string]any{
				"type":        []any{"string", "null"},
				"enum":        []any{"content", "files_with_matches", "count", nil},
				"description": "Output mode",
				"default":     "files_with_matches",
			},
			"plain": map[string]any{"type": "string", "enum": []any{"a", "b"}},
			"mixed": map[string]any{"type": []any{"string", "integer"}, "enum": []any{"a", float64(1)}},
		},
		"required":             []any{"output_mode", "plain", "mixed"},
		"additionalProperties": false,
	}

	props := SanitizeSchema(schema)["properties"].(map[string]any)

	got, _ := json.Marshal(props["output_mode"], json.Deterministic(true))
	want := `{"default":"files_with_matches","description":"Output mode","enum":["content","files_with_matches","count",null]}`
	if string(got) != want {
		t.Errorf("nullable enum:\n got %s\nwant %s", got, want)
	}

	if got, _ := json.Marshal(props["plain"], json.Deterministic(true)); string(got) != `{"enum":["a","b"],"type":"string"}` {
		t.Errorf("plain enum changed: %s", got)
	}

	if got, _ := json.Marshal(props["mixed"], json.Deterministic(true)); string(got) != `{"enum":["a",1],"type":["string","integer"]}` {
		t.Errorf("non-nullable type list changed: %s", got)
	}

	if _, ok := schema["properties"].(map[string]any)["output_mode"].(map[string]any)["type"]; !ok {
		t.Error("input schema was mutated")
	}
}

// Validate values against both schemas so a rewrite cannot silently weaken
// enums or sibling constraints, and resolve references after the rewrite.
func TestSanitizeStrictSchemaNullableEnumConstraints(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"nullable", `{"type":["string","null"],"enum":["a",null]}`},
		{"null excluded by enum", `{"type":["string","null"],"enum":["a","b"]}`},
		{"null only", `{"type":["string","null"],"enum":[null]}`},
		{"sibling const", `{"type":["string","null"],"enum":["a",null],"const":"a"}`},
		{"local definition", `{"type":["string","null"],"enum":["a",null],"$defs":{"choice":{"type":"string","enum":["a"]}},"$ref":"#/properties/mode/$defs/choice"}`},
		{"existing anyOf", `{"type":["string","null"],"enum":["a","b",null],"anyOf":[{"enum":["a",null]}]}`},
		{"existing anyOf and allOf", `{"type":["string","null"],"enum":["a","b",null],"anyOf":[{"enum":["a","b",null]}],"allOf":[{"enum":["b",null]}]}`},
		{"reference to existing anyOf", `{"type":["string","null"],"enum":["a",null],"anyOf":[{"enum":["a"]},{"type":"null"}],"$ref":"#/properties/mode/anyOf/0"}`},
		{"nullable integer", `{"type":["integer","null"],"enum":[1,null]}`},
		{"nullable number", `{"type":["number","null"],"enum":[1.5,null]}`},
		{"nullable boolean", `{"type":["boolean","null"],"enum":[true,null]}`},
		{"type restricts enum values", `{"type":["string","null"],"enum":["a",1,null]}`},
		{"integer excludes fractions", `{"type":["integer","null"],"enum":[1,1.5,null]}`},
	}

	resolve := func(t *testing.T, data []byte) *jsonschema.Resolved {
		t.Helper()
		var schema jsonschema.Schema
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve %s: %v", data, err)
		}
		return resolved
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var field map[string]any
			if err := json.Unmarshal([]byte(tc.schema), &field); err != nil {
				t.Fatal(err)
			}
			schema := map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"mode": field},
				"required":             []any{"mode"},
				"additionalProperties": false,
			}
			before, err := json.Marshal(schema, json.Deterministic(true))
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(SanitizeSchema(schema), json.Deterministic(true))
			if err != nil {
				t.Fatal(err)
			}
			original := resolve(t, before)
			sanitized := resolve(t, after)
			for _, value := range []any{nil, "a", "b", "c", float64(1), float64(2), float64(1.5), true} {
				instance := map[string]any{"mode": value}
				want := original.Validate(instance) == nil
				got := sanitized.Validate(instance) == nil
				if got != want {
					t.Errorf("value %#v: accepted before=%t after=%t; schema=%s", value, want, got, after)
				}
			}
			unchanged, err := json.Marshal(schema, json.Deterministic(true))
			if err != nil {
				t.Fatal(err)
			}
			if string(unchanged) != string(before) {
				t.Error("input schema was mutated")
			}
		})
	}
}

func TestSanitizeSchemaLosslessNumbers(t *testing.T) {
	for _, useNumber := range []bool{false, true} {
		reader := strings.NewReader(`{"type":"object","properties":{"list":{"type":"array","minItems":1},"choice":{"type":["integer","null"],"enum":[1,2,null]}}}`)
		var opts json.Options
		if useNumber {
			opts = jsonutil.PreserveNumbers
		}
		var schema map[string]any
		if err := json.UnmarshalRead(reader, &schema, opts); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(SanitizeSchema(schema), json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"properties":{"choice":{"enum":[1,2,null]},"list":{"minItems":1,"type":"array"}},"type":"object"}`
		if string(got) != want {
			t.Errorf("UseNumber=%t: got %s, want %s", useNumber, got, want)
		}
	}
	for _, tc := range []struct {
		value   string
		integer bool
	}{
		{"9007199254740993", true}, {"1e400", true}, {"2.0", true}, {"1.0000000000000000001", false}, {"1e-400", false},
	} {
		schema := map[string]any{"type": []any{"integer", "null"}, "enum": []any{jsonutil.Number(tc.value), nil}}
		got := SanitizeSchema(schema)
		_, hasType := got["type"]
		if hasType == tc.integer {
			t.Errorf("%s: incorrect integer classification: %v", tc.value, got)
		}
		if got["enum"].([]any)[0] != jsonutil.Number(tc.value) {
			t.Errorf("number changed: %v", got)
		}
	}
}
