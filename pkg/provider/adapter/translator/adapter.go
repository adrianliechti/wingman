package translator

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

var _ translator.Provider = (*Adapter)(nil)

type Adapter struct {
	completer provider.Completer
}

func FromCompleter(completer provider.Completer) *Adapter {
	return &Adapter{
		completer: completer,
	}
}

func (a *Adapter) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	language := options.Language

	if language == "" {
		language = "en"
	}

	content := []provider.Content{
		provider.TextContent(input.Text),
	}

	if input.File != nil {
		content = []provider.Content{
			provider.FileContent(input.File),
		}
	}

	messages := []provider.Message{
		provider.SystemMessage("Act as a translator. Translate the following text to `" + language + "`. Only return the translation, no other text."),
		{
			Role:    provider.MessageRoleUser,
			Content: content,
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

	return &translator.File{
		Content:     []byte(result.Message.Text()),
		ContentType: "text/plain",
	}, nil
}
