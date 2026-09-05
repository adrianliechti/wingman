package llm

import (
	"context"
	"path"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ extractor.Provider = (*Extractor)(nil)

var contentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".pdf":  "application/pdf",
}

type Extractor struct {
	completer provider.Completer
}

func New(completer provider.Completer) *Extractor {
	return &Extractor{
		completer: completer,
	}
}

func (a *Extractor) Extract(ctx context.Context, input extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	contentType := detectContentType(input)

	if contentType == "" {
		return nil, extractor.ErrUnsupported
	}

	input.ContentType = contentType

	messages := []provider.Message{
		provider.SystemMessage("Extract all text content from the provided file. Transcribe it faithfully in reading order, including tables, without summarizing, translating or describing it. Return only the extracted text, no other commentary."),
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.FileContent(&input),
			},
		},
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range a.completer.Complete(ctx, messages, nil) {
		if err != nil {
			return nil, err
		}

		acc.Add(*completion)
	}

	result := acc.Result()

	return &extractor.Document{
		Text: result.Message.Text(),
	}, nil
}

func detectContentType(file extractor.File) string {
	for _, contentType := range contentTypes {
		if file.ContentType == contentType {
			return contentType
		}
	}

	if file.Name != "" {
		ext := strings.ToLower(path.Ext(file.Name))

		if contentType, ok := contentTypes[ext]; ok {
			return contentType
		}
	}

	return ""
}
