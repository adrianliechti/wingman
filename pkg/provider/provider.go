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

type Tool struct {
	Name        string
	Description string

	Strict *bool

	Parameters map[string]any
}

type ToolResult struct {
	ID string

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

	Schema map[string]any // TODO: Rename to Properties
}

type Usage struct {
	InputTokens  int
	OutputTokens int

	CacheReadInputTokens     int
	CacheCreationInputTokens int
}
