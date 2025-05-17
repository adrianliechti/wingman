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
}
