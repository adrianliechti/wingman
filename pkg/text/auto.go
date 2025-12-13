package text

import (
	"unicode/utf8"
)

// AutoSplitter automatically detects content type and uses appropriate splitter
type AutoSplitter struct {
	SplitterOptions
}

func NewAutoSplitter() AutoSplitter {
	return AutoSplitter{
		SplitterOptions: SplitterOptions{
			ChunkSize:    1500,
			ChunkOverlap: 0,

			Trim:      true,
			Normalize: false,

			LenFunc: utf8.RuneCountInString,
		},
	}
}

func (s *AutoSplitter) Split(text string) []string {
	if IsMarkdown(text) {
		return s.splitAsMarkdown(text)
	}

	return s.splitAsText(text)
}

func (s *AutoSplitter) splitAsMarkdown(text string) []string {
	splitter := MarkdownSplitter{
		SplitterOptions: s.SplitterOptions,
	}

	return splitter.Split(text)
}

func (s *AutoSplitter) splitAsText(text string) []string {
	splitter := TextSplitter{
		SplitterOptions: s.SplitterOptions,
	}

	return splitter.Split(text)
}
