package openai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"golang.org/x/net/http/httpguts"
)

// Generated SDK encoders can treat jsonutil.Number inside a schema as a Go
// string. Encode each opaque schema value with encoding/json/v2 first.
func schemaFields(schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	result := make(map[string]any, len(schema))
	for key, value := range schema {
		data, err := json.Marshal(value, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		result[key] = jsontext.Value(data)
	}
	return result, nil
}

// convertError maps upstream OpenAI / Azure OpenAI errors into a
// *provider.ProviderError, classifying rate limits and extracting a
// Retry-After hint from headers or message text.
//
// Azure rate-limit errors surface their code at `error.code` (e.g.
// "RateLimitReached"); content-filter errors use `innererror.code` but are
// already classified via the top-level `error.code == "content_filter"`.
func convertError(err error) error {
	if err == nil {
		return nil
	}

	// Streaming errors (e.g. rate limits that arrive mid-stream) are wrapped as
	// *ssestream.StreamError with raw JSON in Event.Data. Retry advice can
	// appear in error.headers or in the message even after HTTP 200 was sent.
	if streamErr, ok := errors.AsType[*ssestream.StreamError](err); ok {
		var envelope struct {
			Error struct {
				Type    string         `json:"type"`
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Headers jsontext.Value `json:"headers"`
			} `json:"error"`
		}
		_ = json.Unmarshal(streamErr.Event.Data, &envelope)

		body := envelope.Error
		if body.Message == "" {
			body.Message = streamErr.Message
		}

		// The stream already returned 200; the error's type and code classify it.
		return newProviderError(body.Type, body.Code, body.Message, 0, parseRetryHeaders(body.Headers), err)
	}

	if apierr, ok := errors.AsType[*openai.Error](err); ok {
		// Prefer the clean API message over apierr.Error(), which includes
		// the HTTP method/URL/status and raw JSON body.
		message := apierr.Message
		if message == "" {
			message = apierr.Error()
		}

		var retryAfter time.Duration
		if apierr.Response != nil {
			retryAfter = provider.RetryAfterFromHeaders(apierr.Response.Header)
		}

		upstream := newProviderError(apierr.Type, apierr.Code, message, apierr.StatusCode, retryAfter, err)
		upstream.RawJSON = apierr.RawJSON()
		return upstream
	}

	return err
}

// defaultRateLimitRetry is used when a rate-limit error is detected but the
// upstream provided neither a Retry-After header nor a parseable hint in the
// message (observed on some Azure PTU 429s and shared-tier TPM/RPM errors).
//
// 5s is a pragmatic middle ground: long enough for PTU token buckets to
// refill and to avoid hammering shared-tier quotas, short enough that clients
// with their own backoff policy aren't unduly delayed.
const defaultRateLimitRetry = 5 * time.Second

// newProviderError builds a ProviderError from the upstream error fields,
// classifying rate limits and falling back to a retry hint parsed
// from the message when no header was provided. statusCode is 0 for an error
// that arrived inside a stream.
func newProviderError(errType, errCode, message string, statusCode int, retryAfter time.Duration, cause error) *provider.ProviderError {
	kind := errorKind(errType, errCode, statusCode)

	// Prefer the upstream `code` (e.g. "insufficient_quota",
	// "context_length_exceeded"); fall back to `type`.
	t := errCode
	if t == "" {
		t = errType
	}

	if retryAfter <= 0 && kind != provider.ErrorKindQuotaExceeded && kind != provider.ErrorKindBilling {
		retryAfter = parseRetryFromMessage(message)
	}

	if retryAfter <= 0 && kind == provider.ErrorKindRateLimited {
		retryAfter = defaultRateLimitRetry
	}

	return &provider.ProviderError{
		Kind:       kind,
		Type:       t,
		Vendor:     provider.VendorOpenAI,
		Message:    message,
		RetryAfter: retryAfter,
		Err:        cause,
	}
}

// isRateLimitError reports whether the given API error type/code indicates a
// rate-limit condition.
//
// Covers OpenAI platform errors ("rate_limit_exceeded")
// as well as Azure OpenAI variants: PTU sometimes returns numeric codes like
// "429" or Azure-specific innererror codes like "RateLimitReached" /
// "TokensRateLimit" / "RequestRateLimit".
func isRateLimitError(errType, errCode string) bool {
	switch strings.ToLower(errType) {
	case "too_many_requests",
		"rate_limit_error",
		"rate_limit_exceeded",
		"requests",
		"tokens":
		return true
	}

	switch strings.ToLower(errCode) {
	case "rate_limit_exceeded",
		"slow_down",
		"rate_limit_reached",
		"too_many_requests",
		"toomanyrequests",
		"ratelimitreached",
		"tokensratelimit",
		"requestsratelimit",
		"requestratelimit",
		"429":
		return true
	}

	return false
}

// Billing and spend limits also use 429, but waiting does not restore access.
func isQuotaError(code string) bool {
	switch strings.ToLower(code) {
	case "insufficient_quota", "credit_balance_exhausted", "organization_spend_limit_exceeded",
		"project_spend_limit_exceeded", "organization_usage_limit_exceeded":
		return true
	}
	return false
}

// retryMsgRE extracts a retry hint from error messages. Covers real-world
// phrasings from OpenAI ("Please try again in 6s", "retry in 12.345s") and
// Azure OpenAI PTU ("Please retry after 1101 milliseconds") when no
// Retry-After header is present.
var retryMsgRE = regexp.MustCompile(
	`(?i)(?:retry|try\s+again)\s+(?:after|in)\s+(\d+(?:\.\d+)?)\s*(milliseconds|millisecond|ms|seconds|second|secs|sec|s)\b`,
)

// parseRetryFromMessage extracts a retry duration from free-form error text
// as a fallback for upstreams that don't return Retry-After headers.
func parseRetryFromMessage(msg string) time.Duration {
	m := retryMsgRE.FindStringSubmatch(msg)
	if len(m) != 3 {
		return 0
	}

	switch strings.ToLower(m[2]) {
	case "ms", "millisecond", "milliseconds":
		delay, _ := time.ParseDuration(m[1] + "ms")
		return delay
	default:
		delay, _ := time.ParseDuration(m[1] + "s")
		return delay
	}
}

// parseRetryHeaders reads the header object embedded in a streamed error.
// Validate and canonicalize names/values just like HTTP headers; ignore
// malformed objects and unsupported values without losing the error itself.
func parseRetryHeaders(raw jsontext.Value) time.Duration {
	var values map[string]jsontext.Value
	if json.Unmarshal(raw, &values) != nil {
		return 0
	}

	headers := make(http.Header)
	for name, rawValue := range values {
		if !httpguts.ValidHeaderFieldName(name) {
			continue
		}
		var value string
		if json.Unmarshal(rawValue, &value) != nil {
			var number jsonutil.Number
			if json.Unmarshal(rawValue, &number) != nil {
				continue
			}
			value = number.String()
		}
		if httpguts.ValidHeaderFieldValue(value) {
			headers.Set(name, value)
		}
	}
	return provider.RetryAfterFromHeaders(headers)
}

// errorKind classifies an OpenAI error. A quota and a rate limit share 429,
// so their codes refine any status. Otherwise a response status decides; a
// streamed error has none, so its code and type classify it.
// https://developers.openai.com/api/reference/resources/responses/streaming-events
func errorKind(errType, errCode string, statusCode int) provider.ErrorKind {
	switch {
	case isQuotaError(errType) || isQuotaError(errCode):
		return provider.ErrorKindQuotaExceeded
	case isRateLimitError(errType, errCode):
		return provider.ErrorKindRateLimited
	case errType == "service_unavailable_error" || errCode == "server_is_overloaded" || errCode == "service_unavailable_error":
		return provider.ErrorKindUnavailable
	}

	if kind := provider.ErrorKindForStatus(statusCode); kind != "" {
		return kind
	}

	switch errCode {
	case "invalid_api_key":
		return provider.ErrorKindUnauthenticated
	case "unsupported_country_region_territory":
		return provider.ErrorKindPermissionDenied
	case "model_not_found":
		return provider.ErrorKindNotFound
	case "vector_store_timeout":
		return provider.ErrorKindDeadlineExceeded
	case "server_error":
		return provider.ErrorKindInternal
	// context_length_exceeded is undocumented for response.failed but sent in practice.
	case "invalid_prompt", "context_length_exceeded", "data_residency_mismatch", "bio_policy", "misalignment_policy_violation",
		"invalid_image", "invalid_image_format", "invalid_base64_image", "invalid_image_url",
		"image_too_large", "image_too_small", "image_parse_error", "image_content_policy_violation",
		"invalid_image_mode", "image_file_too_large", "unsupported_image_media_type",
		"empty_image_file", "failed_to_download_image", "image_file_not_found":
		return provider.ErrorKindInvalidArgument
	}

	switch errType {
	case "invalid_request_error":
		return provider.ErrorKindInvalidArgument
	case "authentication_error":
		return provider.ErrorKindUnauthenticated
	case "permission_error":
		return provider.ErrorKindPermissionDenied
	case "not_found_error":
		return provider.ErrorKindNotFound
	}

	return provider.ErrorKindInternal
}

// ensureStrictSchema rewrites a JSON schema into the subset OpenAI's strict
// mode accepts: every object closes additionalProperties, lists all of its
// properties as required, and expresses formerly optional properties as
// nullable — the documented way to model optional fields under strict mode.
// Schemas that already satisfy strict mode come back unchanged. The input is
// copied along modified paths, never mutated.
func ensureStrictSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}

	result := maps.Clone(schema)

	schemaType, _ := schema["type"].(string)

	if schemaType == "object" {
		if _, ok := result["additionalProperties"]; !ok {
			result["additionalProperties"] = false
		}

		props, _ := schema["properties"].(map[string]any)

		required := map[string]bool{}
		if list, ok := schema["required"].([]any); ok {
			for _, r := range list {
				if name, ok := r.(string); ok {
					required[name] = true
				}
			}
		}
		if list, ok := schema["required"].([]string); ok {
			for _, name := range list {
				required[name] = true
			}
		}

		if len(props) > 0 {
			newProps := make(map[string]any, len(props))
			names := slices.Sorted(maps.Keys(props))

			for _, name := range names {
				prop, ok := props[name].(map[string]any)
				if !ok {
					newProps[name] = props[name]
					continue
				}

				prop = ensureStrictSchema(prop)

				if !required[name] {
					prop = nullableSchema(prop)
				}

				newProps[name] = prop
			}

			result["properties"] = newProps

			all := make([]any, 0, len(names))
			for _, name := range names {
				all = append(all, name)
			}
			result["required"] = all
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		result["items"] = ensureStrictSchema(items)
	}

	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if arr, ok := schema[key].([]any); ok {
			sub := make([]any, len(arr))
			for i, item := range arr {
				if m, ok := item.(map[string]any); ok {
					sub[i] = ensureStrictSchema(m)
				} else {
					sub[i] = item
				}
			}
			result[key] = sub
		}
	}

	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := schema[key].(map[string]any); ok {
			sub := make(map[string]any, len(defs))
			for name, def := range defs {
				if m, ok := def.(map[string]any); ok {
					sub[name] = ensureStrictSchema(m)
				} else {
					sub[name] = def
				}
			}
			result[key] = sub
		}
	}

	return result
}

// nullableSchema widens a property schema so null is an accepted value.
func nullableSchema(schema map[string]any) map[string]any {
	result := maps.Clone(schema)

	switch t := schema["type"].(type) {
	case string:
		if t != "null" {
			result["type"] = []any{t, "null"}
		}
		return result

	case []any:
		if !slices.Contains(t, any("null")) {
			result["type"] = append(slices.Clone(t), "null")
		}
		return result
	}

	if arr, ok := schema["anyOf"].([]any); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok && m["type"] == "null" {
				return result
			}
		}
		result["anyOf"] = append(slices.Clone(arr), map[string]any{"type": "null"})
		return result
	}

	// $ref or untyped — wrap so the reference stays intact
	if _, ok := schema["$ref"]; ok {
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}

	return result
}

var LegacyModels = []string{
	// GPT 3.5 Family
	"gpt-3.5-turbo",

	// GPT 4 Family
	"gpt-4",
	"gpt-4-turbo",

	// GPT 4o Family
	"gpt-4o",
	"gpt-4o-mini",

	// GPT 4.1 Family
	"gpt-4.1",
	"gpt-4.1-mini",
	"gpt-4.1-nano",
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

func isGPT62(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-6.2-astra") || strings.HasPrefix(m, "gpt-6.2-sol")
}

func compatibleReasoningEffort(model string, effort provider.Effort) provider.Effort {
	m := strings.ToLower(model)

	switch effort {
	case "none":
		if strings.HasPrefix(m, "gpt-6-astra") || strings.HasPrefix(m, "gpt-6.1-sol") ||
			isGPT62(m) {
			return provider.EffortLow
		}
	case provider.EffortMinimal:
		if strings.HasPrefix(m, "gpt-6") {
			return provider.EffortLow
		}
	}

	return effort
}

// normalizedReasoningEffort applies model-specific compatibility rules before
// a request reaches the SDK.
func normalizedReasoningEffort(model string, reasoning *provider.ReasoningOptions) string {
	if reasoning == nil {
		return ""
	}

	if reasoning.Type == provider.ReasoningTypeDisabled {
		return string(compatibleReasoningEffort(model, "none"))
	}

	if reasoning.Effort != "" {
		return string(compatibleReasoningEffort(model, reasoning.Effort))
	}

	if reasoning.Type == provider.ReasoningTypeAdaptive {
		return string(provider.EffortMedium)
	}

	return ""
}
