package harness

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
}
