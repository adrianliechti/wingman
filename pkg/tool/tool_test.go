package tool

import (
	"reflect"
	"testing"
)

func TestNormalizeSchema_PreservesConstraintsAndOriginal(t *testing.T) {
	original := map[string]any{
		"properties":           map[string]any{"query": map[string]any{"type": "string"}},
		"required":             []string{"query"},
		"anyOf":                []map[string]any{{"required": []string{"query"}}},
		"additionalProperties": false,
	}
	got := NormalizeSchema(original)
	if got["type"] != "object" || original["type"] != nil {
		t.Fatal("normalization changed the original or did not infer object type")
	}
	for _, key := range []string{"properties", "required", "anyOf", "additionalProperties"} {
		if !reflect.DeepEqual(got[key], original[key]) {
			t.Fatalf("lost %s", key)
		}
	}
	for _, schema := range []map[string]any{nil, {}, {"type": "object"}} {
		if NormalizeSchema(schema)["properties"] == nil {
			t.Fatal("object schema lacks properties")
		}
	}
}
