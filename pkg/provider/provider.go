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
	Name        string
	Description string

	Kind ToolKind

	Strict     *bool
	Parameters map[string]any

	Display *Display
	Format  *ToolFormat
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

type Usage struct {
	InputTokens  int
	OutputTokens int

	CacheReadInputTokens     int
	CacheCreationInputTokens int
}
