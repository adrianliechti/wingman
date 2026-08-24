package kernel

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode"

	gokernel "github.com/adrianliechti/go-kernel"

	"github.com/adrianliechti/wingman/pkg/extractor"
	textextractor "github.com/adrianliechti/wingman/pkg/extractor/text"
	"github.com/adrianliechti/wingman/pkg/text"
)

var _ extractor.Provider = &Extractor{}

type Extractor struct {
	kernel *gokernel.Kernel
}

func New() (*Extractor, error) {
	e := &Extractor{}

	kernelOptions := gokernel.Options{
		DiscardAttachmentData: true,
		MaxDepth:              32,
		Extractors:            []gokernel.Extractor{textAdapter{}},
	}
	kernelOptions.OOXML.SkipImages = true
	kernelOptions.OOXML.SlideNotes = true

	e.kernel = gokernel.New(kernelOptions)

	return e, nil
}

func (e *Extractor) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	document, err := e.kernel.Extract(ctx, gokernel.Input{
		Name:      file.Name,
		MediaType: file.ContentType,
		Data:      file.Content,
	})

	if err != nil {
		if errors.Is(err, gokernel.ErrUnsupportedFormat) {
			return nil, extractor.ErrUnsupported
		}

		return nil, err
	}

	if document.Format == gokernel.FormatEML && !hasMessageIdentity(document) {
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
	case gokernel.FormatPDF:
		pageCount = document.Metadata["page_count"]
	case gokernel.FormatPPTX:
		pageCount = document.Metadata["slide_count"]
	}

	if count, err := strconv.Atoi(pageCount); err == nil && count > 0 {
		for page := 1; page <= count; page++ {
			result.Pages = append(result.Pages, extractor.Page{Page: page})
		}
	}

	return result, nil
}

func hasMessageIdentity(document *gokernel.Document) bool {
	for _, key := range []string{"from", "to", "cc", "bcc", "reply_to", "message_id"} {
		if strings.TrimSpace(document.Metadata[key]) != "" {
			return true
		}
	}

	return false
}

func needsOCR(document *gokernel.Document, content string) bool {
	if document.Format != gokernel.FormatPDF {
		return false
	}

	return strings.TrimSpace(document.Metadata["pages_needing_ocr"]) != "" ||
		strings.EqualFold(document.Metadata["pdf_type"], "scanned") ||
		!isUsable(content)
}

func renderDocument(document *gokernel.Document) string {
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

func (textAdapter) Supports(input gokernel.Input) bool {
	for _, extension := range textextractor.SupportedExtensions {
		if strings.HasSuffix(strings.ToLower(input.Name), extension) {
			return true
		}
	}

	return slices.Contains(textextractor.SupportedMimeTypes, input.MediaType)
}

func (textAdapter) Extract(ctx context.Context, input gokernel.Input) (*gokernel.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &gokernel.Document{
		Name:      input.Name,
		Format:    gokernel.FormatUnknown,
		MediaType: input.MediaType,
		Markdown:  text.Normalize(string(input.Data)),
	}, nil
}

var _ gokernel.Extractor = textAdapter{}
