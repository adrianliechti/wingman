package anthropic

import (
	"encoding/json"
	"errors"
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

func isAdaptiveThinkingModel(model string) bool {
	model = strings.ToLower(model)

	thinkingPatterns := []string{
		"sonnet-4-6",

		"opus-4-7",
		"opus-4-6",
	}

	for _, p := range thinkingPatterns {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}

func isCompactionSupportedModel(model string) bool {
	model = strings.ToLower(model)

	compactionPatterns := []string{
		"sonnet-4-6",

		"opus-4-7",
		"opus-4-6",
	}

	for _, p := range compactionPatterns {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}
