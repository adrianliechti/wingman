package anthropic

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/claude"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func convertError(err error) error {
	var apierr *anthropic.Error

	if errors.As(err, &apierr) {
		message, errType := extractAnthropicErrorInfo(apierr)
		code := apierr.StatusCode
		if code < 400 {
			// SSE and Bedrock EventStream errors carry the successful stream's
			// HTTP status, not the status of the failure inside that stream.
			code = statusCodeFromErrorType(errType)
		}

		provErr := &provider.ProviderError{
			Code:    code,
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

// extractAnthropicErrorInfo reads Anthropic's nested error envelope or Bedrock's
// top-level message and exception type. The SDK's Error.Error() includes the HTTP
// method, URL, status, Request-ID, and the raw body — too noisy to surface
// as-is to API clients. Falls back to apierr.Error() if parsing fails.
func extractAnthropicErrorInfo(apierr *anthropic.Error) (message, errType string) {
	raw := apierr.RawJSON()

	if raw != "" {
		var payload struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"__type"`
			Error   struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			message = strings.TrimSpace(payload.Error.Message)
			errType = strings.TrimSpace(payload.Error.Type)
			if message == "" {
				message = strings.TrimSpace(payload.Message)
			}
			if errType == "" {
				if apierr.Response != nil {
					errType = strings.TrimSpace(apierr.Response.Header.Get("X-Amzn-Errortype"))
				}
				if errType == "" {
					errType = strings.TrimSpace(payload.Code)
				}
				if errType == "" {
					errType = strings.TrimSpace(payload.Type)
				}
				// AWS exception identifiers can have a namespace and URL suffix.
				errType, _, _ = strings.Cut(errType, ":")
				if _, name, ok := strings.Cut(errType, "#"); ok {
					errType = name
				}
			}
		}
	}

	if message == "" {
		message = apierr.Error()
	}

	return message, errType
}

// statusCodeFromErrorType supplies a failure status for an in-stream error.
// Unknown types remain upstream failures, allowing new provider errors without
// accidentally treating them as successful responses or invalid user input.
func statusCodeFromErrorType(errType string) int {
	switch strings.ToLower(errType) {
	case "invalid_request_error", "validationexception":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "billing_error":
		return http.StatusPaymentRequired
	case "permission_error", "accessdeniedexception":
		return http.StatusForbidden
	case "not_found_error", "resourcenotfoundexception":
		return http.StatusNotFound
	case "conflict_error", "conflictexception":
		return http.StatusConflict
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "rate_limit_error", "throttlingexception":
		return http.StatusTooManyRequests
	case "overloaded_error":
		return 529
	case "serviceunavailableexception":
		return http.StatusServiceUnavailable
	case "timeout_error", "modeltimeoutexception":
		// Match the native Bedrock provider's upstream timeout classification.
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
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

// jsonModeInstruction emulates schema-less JSON mode, which the Messages API
// does not offer natively.
const jsonModeInstruction = "Respond with a single valid JSON object only. Do not wrap it in markdown code fences and do not add any text before or after the JSON."

var LegacyModels = []string{
	"claude-3",

	"sonnet-4-0",
	"sonnet-4-5",

	"opus-4-0",
	"opus-4-1",
	"opus-4-5",

	"haiku-4-5",
}

func outputEffort(e provider.Effort) anthropic.BetaOutputConfigEffort {
	return anthropic.BetaOutputConfigEffort(claude.OutputEffort(e))
}

// progressNotes reports whether returned thinking text holds only the notes
// the model writes between tool calls: display "updates" and between_tools
// return those notes and never reasoning.
func progressNotes(t anthropic.BetaThinkingConfigParamUnion) bool {
	return t.OfBetweenTools != nil || (t.OfAdaptive != nil && t.OfAdaptive.Display == anthropic.BetaThinkingConfigAdaptiveDisplayUpdates)
}

// thinkingReasoning returns progress notes as a summary, so frontends show
// them without the caller asking for reasoning summaries.
func thinkingReasoning(text, signature string, notes bool) provider.Reasoning {
	if notes {
		return provider.Reasoning{Summary: provider.TextParts(text), Signature: signature}
	}
	return provider.Reasoning{Content: provider.TextParts(text), Signature: signature}
}

// thinking is the resolved thinking configuration for one request.
type thinking struct {
	Enabled    bool
	Disabled   bool
	Summarized bool
	Updates    bool
	Effort     anthropic.BetaOutputConfigEffort
}

// resolveThinking maps the reasoning options onto the Messages API. forced
// marks a request that forces a tool call, which thinking is incompatible
// with.
func (c *Completer) resolveThinking(options *provider.CompleteOptions, forced bool) thinking {
	if claude.MatchesModel(c.model, LegacyModels) {
		return thinking{}
	}
	resolved := claude.ResolveThinking(c.model, options.ReasoningOptions, forced)
	return thinking{
		Enabled:    resolved.Enabled,
		Disabled:   resolved.Disabled,
		Summarized: resolved.Summarized,
		Updates:    resolved.Updates,
		Effort:     anthropic.BetaOutputConfigEffort(resolved.Effort),
	}
}
