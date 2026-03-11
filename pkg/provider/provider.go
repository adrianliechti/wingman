package provider

type Provider = any

type Model struct {
	ID string
}

type File struct {
	Name string

	Content     []byte
	ContentType string
}

type ToolType string

const (
	ToolTypeFunction   ToolType = ""
	ToolTypeTextEditor ToolType = "text_editor"
)

type Tool struct {
	Type ToolType

	Name        string
	Description string

	Strict *bool

	Parameters map[string]any
}

type ToolResult struct {
	ID string

	Data string
}

type Schema struct {
	Name        string
	Description string

	Strict *bool

	Schema map[string]any // TODO: Rename to Properties
}

type Usage struct {
	InputTokens  int
	OutputTokens int

	CacheReadInputTokens     int
	CacheCreationInputTokens int
}
