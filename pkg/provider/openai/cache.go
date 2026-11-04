package openai

import (
	"encoding/json/v2"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
)

func cacheWriteDetails(raw string) *provider.CacheWriteTokensDetails {
	var details *struct {
		Standard int `json:"ttl_30m_tokens"`
		Extended int `json:"ttl_12h_tokens"`
	}
	if json.Unmarshal([]byte(raw), &details) != nil || details == nil {
		return nil
	}
	return &provider.CacheWriteTokensDetails{StandardTokens: details.Standard, ExtendedTokens: details.Extended}
}

// supportsCacheBreakpoints reports models with explicit prompt cache
// breakpoints (GPT-5.6 and later). Earlier models cache implicitly only, so
// breakpoints and the explicit mode are dropped for them.
func supportsCacheBreakpoints(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5.6") || strings.HasPrefix(m, "gpt-6")
}

func inputTextBreakpoint(control *provider.CacheControl) responses.ResponseInputTextPromptCacheBreakpointParam {
	result := responses.ResponseInputTextPromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
	if ttl := cacheTTL(control.Retention); ttl != "" {
		result.SetExtraFields(map[string]any{"ttl": ttl})
	}
	return result
}

func inputImageBreakpoint(control *provider.CacheControl) responses.ResponseInputImagePromptCacheBreakpointParam {
	result := responses.ResponseInputImagePromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
	if ttl := cacheTTL(control.Retention); ttl != "" {
		result.SetExtraFields(map[string]any{"ttl": ttl})
	}
	return result
}

func cacheTTL(retention provider.CacheRetention) string {
	switch retention {
	case provider.CacheRetentionStandard:
		return "30m"
	case provider.CacheRetentionExtended:
		return "12h"
	default:
		return ""
	}
}

func chatTextBreakpoint() openai.ChatCompletionContentPartTextPromptCacheBreakpointParam {
	return openai.ChatCompletionContentPartTextPromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
}
