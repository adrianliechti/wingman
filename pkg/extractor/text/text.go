package text

import (
	"context"
	"path"
	"slices"
	"strings"
	"unicode"

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

	if !detectText(file) {
		return nil, extractor.ErrUnsupported
	}

	text := text.Normalize(string(file.Content))

	return &extractor.Document{
		Text: text,
	}, nil
}

func detectText(input extractor.File) bool {
	if isSupported(input) {
		return true
	}

	var printableCount int

	for _, b := range input.Content {
		if b == 0 {
			return false
		}

		if unicode.IsPrint(rune(b)) || b == '\n' || b == '\r' || b == '\t' {
			printableCount++
		}
	}

	return printableCount > (len(input.Content) * 90 / 100)
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
