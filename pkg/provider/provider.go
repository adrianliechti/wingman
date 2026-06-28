package provider

type Model struct {
	ID string
}

type File struct {
	Name string

	Content     []byte
	ContentType string
}

type Tool struct {
	Kind ToolKind

	// Dialect disambiguates built-in tools sharing a name across protocols
	// ("computer"). Empty means the backend's own dialect.
	Dialect string

	Name      string
	Namespace string

	Description string

	Strict   *bool
	Deferred *bool

	Execution  string
	Parameters map[string]any

	Tools []Tool

	Format  *ToolFormat
	Display *Display

	MaxCharacters int
}

type ToolFormat struct {
	Type       string
	Syntax     string
	Definition string
}

type ToolKind string

const (
	ToolKindFunction   ToolKind = ""
	ToolKindCustom     ToolKind = "custom"
	ToolKindTextEditor ToolKind = "text_editor"
	ToolKindComputer   ToolKind = "computer"
	ToolKindShell      ToolKind = "shell"
	ToolKindToolSearch ToolKind = "tool_search"
)

type Display struct {
	Width  int
	Height int

	// Environment is "browser" / "ubuntu" / "windows" / "mac" for the OpenAI
	// Responses computer tool. Anthropic's computer tool ignores it.
	Environment string
}

type ToolResult struct {
	ID string

	Kind ToolKind

	// Execution is set on tool_search_output results to indicate "server"
	// (hosted) or "client" (BYOT) execution.
	Execution string

	// Payload carries opaque JSON for tool kinds with structured result data
	// (tool_search_output's `tools` array, computer_call_output's
	// acknowledged safety checks).
	Payload []byte

	Parts []Part
}

// Part is a leaf piece of content that can appear inside a tool result.
// Either Text or File is set; File covers image / audio / pdf / other
// media via its ContentType.
type Part struct {
	Text string
	File *File
}

type Schema struct {
	Name        string
	Description string

	Strict *bool

	Properties map[string]any
}

// Usage reports token counts in a provider-neutral form. InputTokens and
// OutputTokens are inclusive totals: InputTokens counts every prompt token
// (including cached ones), and OutputTokens counts every generated token
// (including reasoning ones). The remaining fields are subsets of those totals:
//
//	CacheReadInputTokens + CacheCreationInputTokens <= InputTokens
//	ReasoningTokens <= OutputTokens
//
// Each provider's mapping normalizes its native shape to this convention so the
// server handlers can translate to any wire format without per-provider quirks.
type Usage struct {
	InputTokens  int
	OutputTokens int

	ReasoningTokens int

	CacheReadInputTokens     int
	CacheCreationInputTokens int
}
