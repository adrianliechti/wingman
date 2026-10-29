package bedrock

import (
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/claude"
)

// LegacyModels take neither adaptive thinking nor an effort. Bedrock names
// Sonnet 4 and Opus 4 by date only (claude-sonnet-4-20250514).
var LegacyModels = []string{
	"claude-3",

	"sonnet-4-0",
	"sonnet-4-2025",
	"sonnet-4-5",

	"opus-4-0",
	"opus-4-2025",
	"opus-4-1",
	"opus-4-5",

	"haiku-4-5",
}

// ContextBoundModels predate Claude 4.5 and reject a request whose input plus
// max_tokens exceeds the context window, where later models stop at the
// window instead.
var ContextBoundModels = []string{
	"claude-3",

	"sonnet-4-0",
	"sonnet-4-2025",

	"opus-4-0",
	"opus-4-2025",
	"opus-4-1",
}

// defaultMaxTokens is the output limit sent when the client sets none.
// Converse otherwise applies a far lower model default (4096 on Claude Opus
// 5.5), which thinking and a short preamble can exhaust before the tool call,
// so the turn ends as truncated right after the commentary. Claude models get
// the Anthropic adapter's defaults. Other families keep the Bedrock default,
// as do context-bound models, where a large limit would fail long prompts.
func defaultMaxTokens(model string) int32 {
	switch {
	case !isClaudeModel(model) || claude.MatchesModel(model, ContextBoundModels):
		return 0
	case claude.MatchesModel(model, LegacyModels):
		return 64000
	default:
		return 128000
	}
}

// progressNotes reports whether returned thinking text holds only the notes
// the model writes between tool calls: display "updates" and between_tools
// return those notes and never reasoning.
func (c *Completer) progressNotes(t thinking) bool {
	return (t.Enabled && t.Updates) || (t.Disabled && claude.MatchesModel(c.model, claude.BetweenToolsModels))
}

// thinkingReasoning returns progress notes as a summary, so frontends show
// them without the caller asking for reasoning summaries.
func thinkingReasoning(text string, notes bool) provider.Reasoning {
	if notes {
		return provider.Reasoning{Summary: provider.TextParts(text)}
	}
	return provider.Reasoning{Content: provider.TextParts(text)}
}

// schemaToolInstruction steers schema mode toward the schema tool on models
// that cannot be forced to call it.
const schemaToolInstruction = "Always deliver the final answer by calling this tool, and do not answer in plain text."

// Structured outputs (strict tool use) is supported by the Claude 4.5 and 4.6
// models. Newer ones (4.7, 4.8, 5.x) reject the field outright with
// "tools.N.custom.strict: Extra inputs are not permitted", so this is an
// allowlist: omitting strict degrades to unconstrained tool calls, while
// sending it to a model that lacks support fails the whole request.
var StrictToolModels = []string{
	"sonnet-4-5",
	"sonnet-4-6",

	"opus-4-5",
	"opus-4-6",

	"haiku-4-5",
}

func supportsStrictTools(model string) bool {
	return !isClaudeModel(model) || claude.MatchesModel(model, StrictToolModels)
}

// Native JSON-schema output (outputConfig.textFormat) is the same structured
// outputs feature as strict tools, so it follows the Claude allowlist. Other
// model families keep the forced-tool emulation, which needs no structured
// outputs support and therefore cannot fail the request.
func supportsOutputFormat(model string) bool {
	return claude.MatchesModel(model, StrictToolModels)
}

// PreservedThinkingModels retain all prior thinking by default, so accepting
// reasoning.context=all_turns requires no unsupported Converse parameter.
// Entries are substring patterns: opus-5, sonnet-5, and haiku-5 include their 5.5
// models, including regional prefixes and dated Bedrock identifiers.
// Earlier Sonnet/Opus models and Haiku through 4.5 retain only the last turn.
// https://platform.claude.com/docs/en/build-with-claude/context-editing
var PreservedThinkingModels = []string{
	"fable-5",
	"mythos-5",
	"mythos-preview",

	"opus-4-5",
	"opus-4-6",
	"opus-4-7",
	"opus-4-8",
	"opus-5",

	"sonnet-4-6",
	"sonnet-5",

	"haiku-5",
}

type thinking = claude.Thinking

// resolveThinking maps the reasoning options onto the Converse additional
// fields. forced marks a request that forces a tool call (schema mode, tool
// choice "any"), which thinking is incompatible with over Bedrock.
func (c *Completer) resolveThinking(options *provider.CompleteOptions, forced bool) thinking {
	if claude.MatchesModel(c.model, LegacyModels) {
		return thinking{}
	}
	return claude.ResolveThinking(c.model, options.ReasoningOptions, forced)
}
