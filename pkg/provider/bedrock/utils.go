package bedrock

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

var LegacyModels = []string{
	"claude-3",

	"sonnet-4-0",
	"sonnet-4-5",

	"opus-4-0",
	"opus-4-1",
	"opus-4-5",

	"haiku-4-5",
}

func isLegacyModel(model string) bool {
	model = strings.ToLower(model)

	for _, p := range LegacyModels {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}

// Structured outputs (strict tool use) is supported by the Claude 4.5 and 4.6
// models. Newer ones (4.7, 4.8, 5.x) reject the field outright with
// "tools.N.custom.strict: Extra inputs are not permitted", so this is an
// allowlist: omitting strict degrades to unconstrained tool calls, while
// sending it to a model that lacks support fails the whole request.
var StrictToolModels = []string{
	"sonnet-4-5",
	"sonnet-4-6",

	"opus-4-5",
	"opus-4-6",

	"haiku-4-5",
}

func supportsStrictTools(model string) bool {
	if !isClaudeModel(model) {
		return true
	}

	model = strings.ToLower(model)

	for _, p := range StrictToolModels {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}

func outputEffort(e provider.Effort) string {
	switch e {
	case provider.EffortMinimal, provider.EffortLow:
		return "low"
	case provider.EffortMedium:
		return "medium"
	case provider.EffortHigh:
		return "high"
	case provider.EffortXHigh:
		return "xhigh"
	case provider.EffortMax:
		return "max"
	}
	return ""
}

func ensureAdditionalPropertiesFalse(schema map[string]any) map[string]any {
	if schema == nil {
		return schema
	}

	schemaType, _ := schema["type"].(string)
	if schemaType == "object" {
		if _, ok := schema["additionalProperties"]; !ok {
			schema["additionalProperties"] = false
		}

		if props, ok := schema["properties"].(map[string]any); ok {
			for key, val := range props {
				if propSchema, ok := val.(map[string]any); ok {
					props[key] = ensureAdditionalPropertiesFalse(propSchema)
				}
			}
		}
	}

	if schemaType == "array" {
		if items, ok := schema["items"].(map[string]any); ok {
			schema["items"] = ensureAdditionalPropertiesFalse(items)
		}
	}

	return schema
}

// Formats the strict-mode grammar compiler supports; others must be stripped.
var strictSupportedFormats = map[string]bool{
	"date-time": true, "time": true, "date": true, "duration": true,
	"email": true, "hostname": true, "uri": true,
	"ipv4": true, "ipv6": true, "uuid": true,
}

// Strict-mode schema validation rejects value-constraint keywords that other
// providers accept (numerical bounds, string lengths, array sizes, custom
// formats). Mirror the official SDKs: strip them client-side and fold them
// into the description so the model still sees the intent. Returns a copied
// schema along modified paths; the input is never mutated.
func sanitizeStrictSchema(schema map[string]any) map[string]any {
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
			if f, ok := value.(float64); ok && f <= 1 {
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
						sub[name] = sanitizeStrictSchema(ps)
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
				result[key] = sanitizeStrictSchema(m)
			} else {
				result[key] = value
			}

		// lists of sub-schemas
		case "anyOf", "allOf", "oneOf", "prefixItems":
			if arr, ok := value.([]any); ok {
				sub := make([]any, len(arr))
				for i, item := range arr {
					if m, ok := item.(map[string]any); ok {
						sub[i] = sanitizeStrictSchema(m)
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

	return result
}
