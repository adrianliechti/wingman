package claude

import "strings"

// AlwaysThinkingModels reject an explicit `thinking: {type: "disabled"}` —
// thinking cannot be turned off on these models.
var AlwaysThinkingModels = []string{
	"fable-5",
	"mythos-5",
	"mythos-preview",

	"opus-5-5",
}

// DefaultThinkingModels think when the thinking field is omitted. Patterns
// match by substring, so the family patterns also cover their 5.5 models.
var DefaultThinkingModels = []string{
	"opus-5",
	"sonnet-5",
	"haiku-5",
}

// NoForcedToolChoiceModels reject `tool_choice: {type: "any"}` and named
// `tool_choice: {type: "tool"}`. Reject these requests rather than weakening
// the caller's requirement to automatic selection.
var NoForcedToolChoiceModels = []string{
	"fable-5-1",
	"fable-5-2",
	"mythos-5-1",
	"mythos-5-2",

	"opus-5-5",
	"sonnet-5-5",
}

// BetweenToolsModels reject `thinking: {type: "disabled"}`; their lowest
// setting, `thinking: {type: "between_tools"}`, turns off up-front thinking
// and is sent instead.
var BetweenToolsModels = []string{
	"sonnet-5-5",
}

// ProgressUpdateModels write progress notes between tool calls, returned
// with `display: "updates"` (beta) while reasoning stays hidden.
var ProgressUpdateModels = []string{
	"fable-5",
	"mythos-5",

	"opus-5-5",
	"sonnet-5-5",
}

// DisabledThinkingEffortCapModels accept `thinking: {type: "disabled"}` (or
// "between_tools") only at effort "high" or below — pairing it with "xhigh"
// or "max" returns a 400. Patterns match by substring, so "opus-5" also
// covers Opus 5.5, which is always thinking and never reaches the cap.
var DisabledThinkingEffortCapModels = []string{
	"opus-5",
	"sonnet-5-5",
	"haiku-5",
}

// MatchesModel matches model identifiers, including Bedrock prefixes and dates.
func MatchesModel(model string, patterns []string) bool {
	model = strings.ToLower(model)

	for _, p := range patterns {
		if strings.Contains(model, p) {
			return true
		}
	}

	return false
}
