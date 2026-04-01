package openai

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
)

func convertError(err error) error {
	var apierr *openai.Error

	if errors.As(err, &apierr) {
		provErr := &provider.ProviderError{
			StatusCode: apierr.StatusCode,
			Message:    apierr.Error(),
			Err:        err,
		}

		if apierr.Response != nil {
			h := apierr.Response.Header
			provErr.RetryAfter = parseRetryAfter(h)
		}

		return provErr
	}

	return err
}

// parseRetryAfter parses Retry-After (seconds, float, HTTP-date) with retry-after-ms as fallback (Azure OpenAI).
func parseRetryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
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
	}

	if v := h.Get("retry-after-ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}

	return 0
}


// ensureAdditionalPropertiesFalse recursively adds additionalProperties: false
// to all object schemas. Required by OpenAI's strict JSON schema validation.
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

var CodingModels = []string{
	// GPT 5.3 Family
	"gpt-5.3-codex",

	// GPT 5.2 Family
	"gpt-5.2-codex",

	// GPT 5.1 Family
	"gpt-5.1-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",

	// GPT 5 Family
	"gpt-5-codex",
}

var ReasoningModels = []string{
	// GPT 5.4 Family
	"gpt-5.4",
	"gpt-5.4-pro",
	"gpt-5.4-mini",
	"gpt-5.4-nano",

	// GPT 5.3 Family
	"gpt-5.3-codex",

	// GPT 5.2 Family
	"gpt-5.2",
	"gpt-5.2-pro",

	"gpt-5.2-codex",

	// GPT 5.1 Family
	"gpt-5.1",

	"gpt-5.1-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",

	// GPT 5 Family
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",

	"gpt-5-codex",

	// GPT o Family
	"o1",
	"o1-mini",
	"o3",
	"o3-mini",
	"o4-mini",
}
