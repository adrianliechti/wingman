package openai

import (
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
)

// supportsCacheBreakpoints reports models with explicit prompt cache
// breakpoints (GPT-5.6 and later). Earlier models cache implicitly only, so
// breakpoints and the explicit mode are dropped for them.
func supportsCacheBreakpoints(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5.6") || strings.HasPrefix(m, "gpt-6")
}

func inputTextBreakpoint() responses.ResponseInputTextPromptCacheBreakpointParam {
	return responses.ResponseInputTextPromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
}

func inputImageBreakpoint() responses.ResponseInputImagePromptCacheBreakpointParam {
	return responses.ResponseInputImagePromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
}

func chatTextBreakpoint() openai.ChatCompletionContentPartTextPromptCacheBreakpointParam {
	return openai.ChatCompletionContentPartTextPromptCacheBreakpointParam{Mode: constant.ValueOf[constant.Explicit]()}
}
