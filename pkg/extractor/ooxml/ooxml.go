package ooxml

import (
	"bytes"
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/adrianliechti/go-ooxml"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ extractor.Provider = &Extractor{}

type Extractor struct {
}

func New() (*Extractor, error) {
	return &Extractor{}, nil
}

func (e *Extractor) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if !detectOOXML(file) {
		return nil, extractor.ErrUnsupported
	}

	result, err := ooxml.Convert(file.Content, ooxml.Options{
		SkipImages: true,
		SlideNotes: true,
	})

	if err != nil {
		if errors.Is(err, ooxml.ErrNotOOXML) || errors.Is(err, ooxml.ErrUnsupportedFormat) {
			return nil, extractor.ErrUnsupported
		}

		return nil, err
	}

	document := &extractor.Document{
		Text: text.Normalize(result.Markdown),
	}

	for i := range result.SlideCount {
		document.Pages = append(document.Pages, extractor.Page{
			Page: i + 1,
		})
	}

	return document, nil
}

func detectOOXML(file extractor.File) bool {
	if isSupported(file) {
		return true
	}

	return bytes.HasPrefix(file.Content, []byte("PK\x03\x04"))
}

func isSupported(file extractor.File) bool {
	if file.Name != "" {
		ext := strings.ToLower(path.Ext(file.Name))

		if slices.Contains(SupportedExtensions, ext) {
			return true
		}
	}

	if file.ContentType != "" {
		if slices.Contains(SupportedMimeTypes, file.ContentType) {
			return true
		}
	}

	return false
}
