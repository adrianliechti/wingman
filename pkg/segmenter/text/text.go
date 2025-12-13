package text

import (
	"context"
	"path"
	"strings"

	"github.com/adrianliechti/wingman/pkg/segmenter"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ segmenter.Provider = &Provider{}

type Provider struct {
}

type Option func(*Provider)

func New(options ...Option) (*Provider, error) {
	p := &Provider{}

	for _, option := range options {
		option(p)
	}

	return p, nil
}

func (p *Provider) Segment(ctx context.Context, input string, options *segmenter.SegmentOptions) ([]segmenter.Segment, error) {
	if options == nil {
		options = new(segmenter.SegmentOptions)
	}

	splitter := p.createSplitter(options)

	segments := []segmenter.Segment{}

	for _, chunk := range splitter.Split(input) {
		segments = append(segments, segmenter.Segment{
			Text: chunk,
		})
	}

	return segments, nil
}

func (p *Provider) createSplitter(options *segmenter.SegmentOptions) text.Splitter {
	// If we have language-specific separators for this file type, use TextSplitter with custom separators
	// This is especially useful for code files where we want to split at meaningful boundaries
	if separators := getSeparators(options.FileName); len(separators) > 0 {
		splitter := text.NewTextSplitter()
		splitter.Separators = separators

		if options.SegmentLength != nil {
			splitter.ChunkSize = *options.SegmentLength
		}

		if options.SegmentOverlap != nil {
			splitter.ChunkOverlap = *options.SegmentOverlap
		}

		return &splitter
	}

	// Otherwise use AutoSplitter which detects markdown vs plain text
	splitter := text.NewAutoSplitter()

	if options.SegmentLength != nil {
		splitter.ChunkSize = *options.SegmentLength
	}

	if options.SegmentOverlap != nil {
		splitter.ChunkOverlap = *options.SegmentOverlap
	}

	return &splitter
}

func getSeparators(name string) []string {
	switch strings.ToLower(path.Ext(name)) {
	case ".cs":
		return languageCSharp
	case ".cpp":
		return languageCPP
	case ".go":
		return languageGo
	case ".java":
		return languageJava
	case ".kt":
		return languageKotlin
	case ".js", ".jsm":
		return languageJavaScript
	case ".ts", ".tsx":
		return languageTypeScript
	case ".py":
		return languagePython
	case ".rb":
		return languageRuby
	case ".rs":
		return languageRust
	case ".sc", ".scala":
		return languageScala
	case ".swift":
		return languageSwift
	}

	return nil
}
