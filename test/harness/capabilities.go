package harness

import "strings"

// Capabilities describes which features a model supports through a given
// wingman API surface. Each surface harness derives its own per-model values.
type Capabilities struct {
	Thinking         bool
	StructuredOutput bool
	Compaction       bool
	TextEditor       bool
	ComputerUse      bool
	Shell            bool
	ToolSearch       bool
	Audio            bool
	Cache            bool

	// NoForcedToolChoice marks models that reject tool_choice "any" and a
	// named tool; only "auto" and "none" are accepted.
	NoForcedToolChoice bool
}

// RejectsForcedToolChoice reports whether a Claude model rejects forced tool
// choice (Fable/Mythos 5.1 and 5.2, Opus 5.5, Sonnet 5.5).
func RejectsForcedToolChoice(name string) bool {
	n := strings.ToLower(name)

	for _, p := range []string{"fable-5-1", "fable-5-2", "mythos-5-1", "mythos-5-2", "opus-5-5", "sonnet-5-5"} {
		if strings.Contains(n, p) {
			return true
		}
	}

	return false
}
