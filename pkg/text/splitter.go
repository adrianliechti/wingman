package text

type Splitter interface {
	Split(text string) []string
}

type SplitterOptions struct {
	ChunkSize    int
	ChunkOverlap int

	Trim      bool
	Normalize bool

	LenFunc func(string) int
}
