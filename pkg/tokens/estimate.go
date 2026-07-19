package tokens

import (
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Input is a request to estimate, in wingman's common provider format. Both
// API surfaces (Anthropic Messages, OpenAI Responses) convert their wire
// formats into this before counting, so cross-model calls — a Claude model
// requested via the Responses API, or a GPT model via the Messages API —
// are estimated identically regardless of the surface that received them.
type Input struct {
	Messages []provider.Message
	Tools    []provider.Tool
}

// Estimate returns the estimated input tokens for serving in with model. The
// model resolves the tokenizer family and the serving provider's measured
// framing constants — the provider that will actually count the tokens, not
// the API dialect of the caller.
func Estimate(model string, in Input) int {
	family := FamilyFor(model)
	anthropicServed := family == Claude2026 || family == ClaudeLegacy

	count := requestOverhead(anthropicServed)

	for _, msg := range in.Messages {
		count += messageOverhead(anthropicServed, msg.Role)
		for _, content := range msg.Content {
			count += contentTokens(model, family, anthropicServed, content)
		}
	}

	if len(in.Tools) > 0 {
		if anthropicServed {
			count += anthropicToolSystemOverhead(family)
		}
		for _, tool := range in.Tools {
			content := textForFamily(family, tool.Name)
			content += textForFamily(family, tool.Description)
			if len(tool.Parameters) > 0 {
				if data, err := json.Marshal(tool.Parameters); err == nil {
					content += textForFamily(family, string(data))
				}
			}
			if anthropicServed {
				// Anthropic renders tool definitions more verbosely than raw
				// JSON and frames each tool (measured 2026-07-19: ~1.25x
				// content plus ~25 tokens per tool).
				count += anthropicPerToolOverhead + content + content/4
			} else {
				count += content
			}
		}
	}

	return count
}

// System prompt Anthropic injects whenever any tool is defined (measured via
// count_tokens, 2026-07-19).
func anthropicToolSystemOverhead(family Family) int {
	if family == Claude2026 {
		return 354
	}
	return 496
}

const anthropicPerToolOverhead = 25

func requestOverhead(anthropicServed bool) int {
	if anthropicServed {
		return AnthropicRequestOverhead
	}
	return OpenAIRequestOverhead
}

func messageOverhead(anthropicServed bool, role provider.MessageRole) int {
	if anthropicServed {
		switch role {
		case provider.MessageRoleUser:
			return AnthropicUserMessageOverhead
		case provider.MessageRoleSystem:
			return 0 // top-level system field: no message wrapping
		default:
			return AnthropicAssistantOverhead
		}
	}
	return OpenAIMessageOverhead
}

func contentTokens(model string, family Family, anthropicServed bool, content provider.Content) int {
	switch {
	case content.Text != "":
		return textForFamily(family, content.Text)

	case content.Refusal != "":
		return textForFamily(family, content.Refusal)

	case content.File != nil:
		return fileTokens(model, family, anthropicServed, content.File)

	case content.Reasoning != nil:
		return textForFamily(family, content.Reasoning.Text) +
			textForFamily(family, content.Reasoning.Summary)

	case content.ToolCall != nil:
		return textForFamily(family, content.ToolCall.Name) +
			textForFamily(family, content.ToolCall.Arguments)

	case content.ToolResult != nil:
		return toolResultTokens(family, content.ToolResult)

	default:
		return 0
	}
}

func fileTokens(model string, family Family, anthropicServed bool, file *provider.File) int {
	switch {
	case strings.HasPrefix(file.ContentType, "image/"):
		w, h, _ := ImageDims(file.Content) // (0,0) on failure: formulas fall back conservatively
		if anthropicServed {
			return ClaudeImage(model, w, h)
		}
		return OpenAIImage(model, w, h, false)

	case file.ContentType == "application/pdf":
		if anthropicServed {
			return PDFPages(file.Content) * ClaudePDFPageTokens
		}
		return PDFPages(file.Content) * OpenAIPDFPageTokens

	case strings.HasPrefix(file.ContentType, "text/"), file.ContentType == "application/json":
		return textForFamily(family, string(file.Content))

	default:
		// Unknown binary payload: rough byte-based floor.
		return len(file.Content) / 4
	}
}

func toolResultTokens(family Family, result *provider.ToolResult) int {
	count := 0
	if len(result.Payload) > 0 {
		count += textForFamily(family, string(result.Payload))
	}
	for _, part := range result.Parts {
		if part.Text != "" {
			count += textForFamily(family, part.Text)
		}
		if part.File != nil {
			count += textForFamily(family, string(part.File.Content))
		}
	}
	return count
}
