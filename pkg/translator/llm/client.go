package llm

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

type Client struct {
	completer provider.Completer
	extractor extractor.Provider
}

func New(completer provider.Completer, extractor extractor.Provider) (*Client, error) {
	c := &Client{
		completer: completer,
		extractor: extractor,
	}

	return c, nil
}

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	text := input.Text
	language := options.Language

	if language == "" {
		language = "en"
	}

	if input.File != nil {
		if c.extractor == nil {
			return nil, errors.New("no extractor configured")
		}

		result, err := c.extractor.Extract(ctx, *input.File, nil)

		if err != nil {
			return nil, err
		}

		text = string(result.Text)
	}

	messages := []provider.Message{
		provider.SystemMessage("Act as a translator. Translate the following text to `" + language + "`. Only return the translation, no other text."),
		provider.UserMessage(text),
	}

	acc := provider.CompletionAccumulator{}

	for completion, err := range c.completer.Complete(ctx, messages, nil) {
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
