package provider

import "strings"

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

// Text returns the concatenated text of all text-bearing parts. Used by
// providers whose wire format can only express a single text tool result.
func (r ToolResult) Text() string {
	var b strings.Builder
	for _, p := range r.Parts {
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
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
