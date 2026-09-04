package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

// convertError maps upstream OpenAI / Azure OpenAI errors into a
// *provider.ProviderError, normalising rate-limit responses to HTTP 429 and
// extracting a Retry-After hint from headers or message text.
//
// Azure rate-limit errors surface their code at `error.code` (e.g.
// "RateLimitReached"); content-filter errors use `innererror.code` but are
// already classified via the top-level `error.code == "content_filter"`.
func convertError(err error) error {
	if err == nil {
		return nil
	}

	// Streaming errors (e.g. rate limits that arrive mid-stream) are wrapped as
	// *ssestream.StreamError with raw JSON in Event.Data. No HTTP headers are
	// accessible (the stream already returned 200 OK), so any Retry-After hint
	// must come from the error body itself (Azure PTU typically embeds one,
	// e.g. "Please retry after 14 seconds").
	if streamErr, ok := errors.AsType[*ssestream.StreamError](err); ok {
		var envelope struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(streamErr.Event.Data, &envelope)

		body := envelope.Error
		if body.Message == "" {
			body.Message = streamErr.Message
		}

		return newProviderError(body.Type, body.Code, body.Message, http.StatusBadGateway, 0, err)
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
			retryAfter = parseRetryAfter(apierr.Response.Header)
		}

		return newProviderError(apierr.Type, apierr.Code, message, apierr.StatusCode, retryAfter, err)
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
// remapping rate-limit errors to 429 and falling back to a retry hint parsed
// from the message when no header was provided.
func newProviderError(errType, errCode, message string, statusCode int, retryAfter time.Duration, cause error) *provider.ProviderError {
	rateLimited := isRateLimitError(errType, errCode)
	if rateLimited {
		statusCode = http.StatusTooManyRequests
	}

	// Prefer the upstream `code` (e.g. "insufficient_quota",
	// "context_length_exceeded"); fall back to `type`.
	t := errCode
	if t == "" {
		t = errType
	}

	if retryAfter <= 0 {
		retryAfter = parseRetryFromMessage(message)
	}

	if retryAfter <= 0 && rateLimited {
		retryAfter = defaultRateLimitRetry
	}

	return &provider.ProviderError{
		Code:       statusCode,
		Type:       t,
		Message:    message,
		RetryAfter: retryAfter,
		Err:        cause,
	}
}

// isRateLimitError reports whether the given API error type/code indicates a
// rate-limit condition that should be mapped to HTTP 429.
//
// Covers OpenAI platform errors ("rate_limit_exceeded", "insufficient_quota")
// as well as Azure OpenAI variants: PTU sometimes returns numeric codes like
// "429" or Azure-specific innererror codes like "RateLimitReached" /
// "TokensRateLimit" / "RequestRateLimit".
func isRateLimitError(errType, errCode string) bool {
	switch strings.ToLower(errType) {
	case "too_many_requests",
		"rate_limit_exceeded",
		"requests",
		"tokens":
		return true
	}

	switch strings.ToLower(errCode) {
	case "rate_limit_exceeded",
		"rate_limit_reached",
		"insufficient_quota",
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

	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}

	switch strings.ToLower(m[2]) {
	case "ms", "millisecond", "milliseconds":
		return time.Duration(val * float64(time.Millisecond))
	default:
		return time.Duration(val * float64(time.Second))
	}
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

// statusCodeFromResponseErrorCode maps a Responses API ResponseErrorCode
// (emitted via response.failed streaming events) to an HTTP status code.
// See: https://platform.openai.com/docs/api-reference/responses-streaming/response/failed
func statusCodeFromResponseErrorCode(code string) int {
	switch code {
	case "rate_limit_exceeded":
		return http.StatusTooManyRequests
	case "server_error", "vector_store_timeout":
		return http.StatusBadGateway
	case "":
		return http.StatusBadGateway
	default:
		// All other documented codes are request-payload issues
		// (invalid_prompt, invalid_image, image_too_large, etc.)
		return http.StatusBadRequest
	}
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

func isGPT6Astra(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "gpt-6-astra")
}

// normalizedReasoningEffort applies model-specific compatibility rules before
// a request reaches the SDK. GPT-6 Astra rejects both `none` and `minimal`;
// OpenAI's migration guidance recommends `low` for either setting.
func normalizedReasoningEffort(model string, reasoning *provider.ReasoningOptions) string {
	if reasoning == nil {
		return ""
	}

	if reasoning.Type == provider.ReasoningTypeDisabled {
		if isGPT6Astra(model) {
			return string(provider.EffortLow)
		}
		return "none"
	}

	effort := reasoning.Effort
	if isGPT6Astra(model) && effort == provider.EffortMinimal {
		effort = provider.EffortLow
	}

	if effort != "" {
		return string(effort)
	}

	if reasoning.Type == provider.ReasoningTypeAdaptive {
		return string(provider.EffortMedium)
	}

	return ""
}
