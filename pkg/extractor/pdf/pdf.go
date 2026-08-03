package pdf

import (
	"bytes"
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/adrianliechti/go-pdf"

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

	if !detectPDF(file) {
		return nil, extractor.ErrUnsupported
	}

	result, err := pdf.Process(file.Content, pdf.Options{})

	if err != nil {
		if errors.Is(err, pdf.ErrNotAPDF) {
			return nil, extractor.ErrUnsupported
		}

		return nil, err
	}

	content := text.Normalize(result.Markdown)

	// A scan or a broken font encoding is not reported as an error: Process
	// succeeds and leaves empty or garbled text behind. Report those as
	// unsupported so a chained OCR extractor gets a turn.
	if result.Type == pdf.TypeScanned || !isUsable(content) {
		return nil, extractor.ErrUnsupported
	}

	document := &extractor.Document{
		Text: content,
	}

	for i := range result.PageCount {
		document.Pages = append(document.Pages, extractor.Page{
			Page: int(i) + 1,
		})
	}

	return document, nil
}

const maxReplacementRatio = 0.5

func isUsable(content string) bool {
	var invalid, total int

	for _, r := range content {
		if unicode.IsSpace(r) {
			continue
		}

		total++

		if r == unicode.ReplacementChar {
			invalid++
		}
	}

	if total == 0 {
		return false
	}

	return float64(invalid)/float64(total) < maxReplacementRatio
}

func detectPDF(file extractor.File) bool {
	if isSupported(file) {
		return true
	}

	return bytes.HasPrefix(file.Content, []byte("%PDF-"))
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
