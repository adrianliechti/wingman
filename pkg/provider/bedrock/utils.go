package bedrock

import (
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
