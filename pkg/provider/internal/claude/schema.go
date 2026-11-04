package claude

import (
	"encoding/json/jsontext"
	"fmt"
	"math"
	"strings"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

// Formats the strict-mode grammar compiler supports; others must be stripped.
var strictSupportedFormats = map[string]bool{
	"date-time": true, "time": true, "date": true, "duration": true,
	"email": true, "hostname": true, "uri": true,
	"ipv4": true, "ipv6": true, "uuid": true,
}

// SanitizeSchema prepares schemas for Claude's strict-mode grammar. It strips
// value-constraint keywords that other
// providers accept (numerical bounds, string lengths, array sizes, custom
// formats). Mirror the official SDKs: strip them client-side and fold them
// into the description so the model still sees the intent. Returns a copied
// schema along modified paths; the input is never mutated.
func SanitizeSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}

	result := make(map[string]any, len(schema))

	var stripped []string

	for key, value := range schema {
		switch key {
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength",
			"maxItems", "uniqueItems", "minContains", "maxContains",
			"minProperties", "maxProperties":
			stripped = append(stripped, fmt.Sprintf("%s %v", key, value))
			continue

		case "format":
			if s, ok := value.(string); ok && !strictSupportedFormats[s] {
				stripped = append(stripped, "format "+s)
				continue
			}
			result[key] = value

		case "minItems":
			// only 0 and 1 are supported
			if supportedMinItems(value) {
				result[key] = value
			} else {
				stripped = append(stripped, fmt.Sprintf("minItems %v", value))
			}

		// maps of named sub-schemas — keys are names, values are schemas
		case "properties", "$defs", "definitions", "patternProperties":
			if m, ok := value.(map[string]any); ok {
				sub := make(map[string]any, len(m))
				for name, propSchema := range m {
					if ps, ok := propSchema.(map[string]any); ok {
						sub[name] = SanitizeSchema(ps)
					} else {
						sub[name] = propSchema
					}
				}
				result[key] = sub
			} else {
				result[key] = value
			}

		// single sub-schema values
		case "items", "contains", "propertyNames", "not", "if", "then", "else":
			if m, ok := value.(map[string]any); ok {
				result[key] = SanitizeSchema(m)
			} else {
				result[key] = value
			}

		// lists of sub-schemas
		case "anyOf", "allOf", "oneOf", "prefixItems":
			if arr, ok := value.([]any); ok {
				sub := make([]any, len(arr))
				for i, item := range arr {
					if m, ok := item.(map[string]any); ok {
						sub[i] = SanitizeSchema(m)
					} else {
						sub[i] = item
					}
				}
				result[key] = sub
			} else {
				result[key] = value
			}

		default:
			result[key] = value
		}
	}

	// keep the model aware of stripped constraints — they are no longer
	// grammar-enforced, but still guide generation
	if len(stripped) > 0 {
		hint := "Constraints: " + strings.Join(stripped, ", ")

		if desc, ok := result["description"].(string); ok && desc != "" {
			result["description"] = desc + " (" + hint + ")"
		} else {
			result["description"] = hint
		}
	}

	return simplifyNullableEnum(result)
}

// Anthropic rejects an enum next to a type list. When every enum value
// already satisfies the nullable type, that type is redundant: removing it
// preserves the enum and all sibling constraints without introducing anyOf.
func simplifyNullableEnum(schema map[string]any) map[string]any {
	types, ok := schema["type"].([]any)
	if !ok {
		return schema
	}

	values, ok := schema["enum"].([]any)
	if !ok {
		return schema
	}

	var others []any
	nullable := false

	for _, t := range types {
		if t == "null" {
			nullable = true
		} else {
			others = append(others, t)
		}
	}

	if !nullable || len(others) != 1 || len(values) == 0 {
		return schema
	}

	for _, v := range values {
		matches := false
		switch v := v.(type) {
		case nil:
			matches = true
		case string:
			matches = others[0] == "string"
		case bool:
			matches = others[0] == "boolean"
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			matches = others[0] == "integer" || others[0] == "number"
		case jsonutil.Number:
			valid := len(v) > 0 && (v[0] == '-' || v[0] >= '0' && v[0] <= '9') && jsontext.Value(v).IsValid()
			matches = valid && (others[0] == "number" || others[0] == "integer" && jsonutil.IsInteger(v))
		case float64:
			matches = others[0] == "number" || others[0] == "integer" && math.Trunc(v) == v
		case float32:
			matches = others[0] == "number" || others[0] == "integer" && math.Trunc(float64(v)) == float64(v)
		}
		if !matches {
			return schema
		}
	}

	result := make(map[string]any, len(schema))

	for key, v := range schema {
		if key != "type" {
			result[key] = v
		}
	}

	return result
}

func supportedMinItems(value any) bool {
	switch value.(type) {
	case jsonutil.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		number, ok := jsonutil.Int64(jsonutil.Number(fmt.Sprint(value)))
		return ok && (number == 0 || number == 1)
	default:
		return false
	}
}
