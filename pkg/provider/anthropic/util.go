package anthropic

import (
	"encoding/json/v2"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/claude"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

func convertError(err error) error {
	if apierr, ok := errors.AsType[*anthropic.Error](err); ok {
		message, errType := extractAnthropicErrorInfo(apierr)
		vendor := provider.VendorAnthropic
		if strings.HasSuffix(strings.ToLower(errType), "exception") {
			vendor = provider.VendorBedrock
		}
		// HTTP statuses classify generic error bodies. An explicit billing
		// failure or a modeled AWS exception can carry a more specific cause.
		kind := errorKind(errType)
		if status := provider.ErrorKindForStatus(apierr.StatusCode); status != "" {
			if kind == "" || (vendor == provider.VendorAnthropic && kind != provider.ErrorKindBilling) {
				kind = status
			}
		}
		if kind == "" {
			kind = provider.ErrorKindInternal
		}

		provErr := &provider.ProviderError{
			Kind:      kind,
			Type:      errType,
			Vendor:    vendor,
			RawJSON:   apierr.RawJSON(),
			RequestID: apierr.RequestID,
			Message:   message,
			Err:       err,
		}

		if apierr.Response != nil {
			provErr.RetryAfter = provider.RetryAfterFromHeaders(apierr.Response.Header)
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
		// Bedrock error envelopes use both "message" and "Message".
		var payload struct {
			Message string `json:"message,case:ignore"`
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

// errorKind classifies Anthropic error types and the AWS exceptions of Claude
// on Bedrock. It returns "" for unknown types, which their status classifies.
func errorKind(errType string) provider.ErrorKind {
	switch strings.ToLower(errType) {
	case "invalid_request_error", "validationexception":
		return provider.ErrorKindInvalidArgument
	case "authentication_error":
		return provider.ErrorKindUnauthenticated
	case "billing_error":
		return provider.ErrorKindBilling
	case "servicequotaexceededexception":
		return provider.ErrorKindQuotaExceeded
	case "permission_error", "accessdeniedexception":
		return provider.ErrorKindPermissionDenied
	case "not_found_error", "resourcenotfoundexception":
		return provider.ErrorKindNotFound
	case "conflict_error", "conflictexception":
		return provider.ErrorKindConflict
	case "request_too_large":
		return provider.ErrorKindPayloadTooLarge
	case "rate_limit_error", "throttlingexception":
		return provider.ErrorKindRateLimited
	case "overloaded_error", "serviceunavailableexception", "modelnotreadyexception":
		return provider.ErrorKindUnavailable
	case "timeout_error", "modeltimeoutexception":
		return provider.ErrorKindDeadlineExceeded
	case "api_error", "internalserverexception", "modelstreamerrorexception":
		return provider.ErrorKindInternal
	}
	return ""
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
