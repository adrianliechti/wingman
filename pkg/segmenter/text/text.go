package text

import (
	"context"

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
	// Try tree-sitter based code splitting first (syntax-aware, 200+ languages)
	if codeSplitter := text.NewCodeSplitter(options.FileName); codeSplitter != nil {
		if options.SegmentLength != nil {
			codeSplitter.ChunkSize = *options.SegmentLength
		}

		if options.SegmentOverlap != nil {
			codeSplitter.ChunkOverlap = *options.SegmentOverlap
		}

		return codeSplitter
	}

	// Fallback to TextSplitter for plain text
	splitter := text.NewTextSplitter()

	if options.SegmentLength != nil {
		splitter.ChunkSize = *options.SegmentLength
	}

	if options.SegmentOverlap != nil {
		splitter.ChunkOverlap = *options.SegmentOverlap
	}

	return &splitter
}

