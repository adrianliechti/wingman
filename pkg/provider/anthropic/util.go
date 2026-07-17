package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/anthropics/anthropic-sdk-go"
)

func convertError(err error) error {
	var apierr *anthropic.Error

	if errors.As(err, &apierr) {
		message, errType := extractAnthropicErrorInfo(apierr)

		provErr := &provider.ProviderError{
			Code:    apierr.StatusCode,
			Type:    errType,
			Message: message,
			Err:     err,
		}

		if apierr.Response != nil {
			h := apierr.Response.Header
			provErr.RetryAfter = parseRetryAfter(h)
		}

		return provErr
	}

	return err
}

// extractAnthropicErrorInfo pulls the clean error.message and error.type out
// of the SDK's raw JSON body (shape: {"error":{"type":"rate_limit_error",
// "message":"..."}}). The SDK's Error.Error() string includes the HTTP
// method, URL, status, Request-ID, and the raw body — too noisy to surface
// as-is to API clients. Falls back to apierr.Error() if parsing fails.
func extractAnthropicErrorInfo(apierr *anthropic.Error) (message, errType string) {
	raw := apierr.RawJSON()

	if raw != "" {
		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			message = strings.TrimSpace(payload.Error.Message)
			errType = strings.TrimSpace(payload.Error.Type)
		}
	}

	if message == "" {
		message = apierr.Error()
	}

	return message, errType
}

// parseRetryAfter parses Retry-After (seconds, float, HTTP-date).
func parseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}

	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}

	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(secs * float64(time.Second))
	}

	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}

	return 0
}

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

// AlwaysThinkingModels reject an explicit `thinking: {type: "disabled"}` —
// thinking cannot be turned off on these models.
var AlwaysThinkingModels = []string{
	"fable-5",
	"mythos-5",
	"mythos-preview",
}

func isAlwaysThinkingModel(model string) bool {
	model = strings.ToLower(model)

	for _, p := range AlwaysThinkingModels {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}

// NoSamplingModels reject temperature/top_p/top_k outright, regardless of
// thinking state. Claude Sonnet 5 only rejects non-default values, but since
// omitting the field has the same effect as passing the default, it is
// treated the same way here rather than guessing what "default" means.
var NoSamplingModels = []string{
	"fable-5",
	"mythos-5",
	"mythos-preview",

	"opus-4-7",
	"opus-4-8",

	"sonnet-5",
}

func isNoSamplingModel(model string) bool {
	model = strings.ToLower(model)

	for _, p := range NoSamplingModels {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}

// disabledThinking returns the thinking config that explicitly turns
// thinking off. Always-thinking models reject `{type: "disabled"}` outright,
// so for those (and legacy models, which don't take a thinking config at
// all) the field is left omitted instead.
func disabledThinking(model string) anthropic.BetaThinkingConfigParamUnion {
	if isLegacyModel(model) || isAlwaysThinkingModel(model) {
		return anthropic.BetaThinkingConfigParamUnion{}
	}

	return anthropic.BetaThinkingConfigParamUnion{
		OfDisabled: &anthropic.BetaThinkingConfigDisabledParam{},
	}
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

func outputEffort(e provider.Effort) anthropic.BetaOutputConfigEffort {
	switch e {
	case provider.EffortMinimal, provider.EffortLow:
		return anthropic.BetaOutputConfigEffortLow
	case provider.EffortMedium:
		return anthropic.BetaOutputConfigEffortMedium
	case provider.EffortHigh:
		return anthropic.BetaOutputConfigEffortHigh
	case provider.EffortXHigh:
		return anthropic.BetaOutputConfigEffortXhigh
	case provider.EffortMax:
		return anthropic.BetaOutputConfigEffortMax
	}
	return ""
}
