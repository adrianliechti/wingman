package extract

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/adrianliechti/go-extract"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/plain"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ extractor.Provider = &Extractor{}

type Extractor struct {
	dispatcher *extract.Dispatcher
}

func New() (*Extractor, error) {
	e := &Extractor{}

	options := extract.Options{
		DiscardAttachmentData: true,
		MaxDepth:              32,
		Extractors:            []extract.Extractor{textAdapter{}},
	}
	options.OOXML.SkipImages = true
	options.OOXML.SlideNotes = true

	e.dispatcher = extract.New(options)

	return e, nil
}

func (e *Extractor) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	document, err := e.dispatcher.Extract(ctx, extract.Input{
		Name:      file.Name,
		MediaType: file.ContentType,
		Data:      file.Content,
	})

	if err != nil {
		if errors.Is(err, extract.ErrUnsupportedFormat) {
			return nil, extractor.ErrUnsupported
		}

		return nil, err
	}

	if document.Format == extract.FormatEML && !hasMessageIdentity(document) {
		return nil, extractor.ErrUnsupported
	}

	content := text.Normalize(renderDocument(document))

	if needsOCR(document, content) {
		return nil, extractor.ErrUnsupported
	}

	result := &extractor.Document{
		Text: content,
	}

	pageCount := ""

	switch document.Format {
	case extract.FormatPDF:
		pageCount = document.Metadata["page_count"]
	case extract.FormatPPTX:
		pageCount = document.Metadata["slide_count"]
	}

	if count, err := strconv.Atoi(pageCount); err == nil && count > 0 {
		for page := 1; page <= count; page++ {
			result.Pages = append(result.Pages, extractor.Page{Page: page})
		}
	}

	return result, nil
}

func hasMessageIdentity(document *extract.Document) bool {
	for _, key := range []string{"from", "to", "cc", "bcc", "reply_to", "message_id"} {
		if strings.TrimSpace(document.Metadata[key]) != "" {
			return true
		}
	}

	return false
}

func needsOCR(document *extract.Document, content string) bool {
	if document.Format != extract.FormatPDF {
		return false
	}

	return strings.TrimSpace(document.Metadata["pages_needing_ocr"]) != "" ||
		strings.EqualFold(document.Metadata["pdf_type"], "scanned") ||
		!isUsable(content)
}

func renderDocument(document *extract.Document) string {
	content := strings.TrimSpace(document.Markdown)
	attachments := make([]string, 0, len(document.Attachments))

	for i := range document.Attachments {
		attachment := &document.Attachments[i]

		if attachment.Document == nil {
			continue
		}

		extracted := strings.TrimSpace(renderDocument(attachment.Document))

		if extracted == "" {
			continue
		}

		attachments = append(attachments, "### "+escapeHeading(attachment.Name)+"\n\n"+extracted)
	}

	if len(attachments) > 0 {
		if content != "" {
			content += "\n\n"
		}

		content += "## Attachment contents\n\n" + strings.Join(attachments, "\n\n")
	}

	return content
}

func escapeHeading(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "#", "\\#")
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

type textAdapter struct{}

func (textAdapter) Supports(input extract.Input) bool {
	for _, extension := range plain.SupportedExtensions {
		if strings.HasSuffix(strings.ToLower(input.Name), extension) {
			return true
		}
	}

	return slices.Contains(plain.SupportedMimeTypes, input.MediaType)
}

func (textAdapter) Extract(ctx context.Context, input extract.Input) (*extract.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &extract.Document{
		Name:      input.Name,
		Format:    extract.FormatUnknown,
		MediaType: input.MediaType,
		Markdown:  text.Normalize(string(input.Data)),
	}, nil
}

var _ extract.Extractor = textAdapter{}
