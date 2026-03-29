package extractor

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ extractor.Provider = (*Adapter)(nil)

type Adapter struct {
	completer provider.Completer
}

func FromCompleter(completer provider.Completer) *Adapter {
	return &Adapter{
		completer: completer,
	}
}

func (a *Adapter) Extract(ctx context.Context, input extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	messages := []provider.Message{
		provider.SystemMessage("Extract and return all text content from the provided file. Return only the extracted text, no other commentary."),
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
