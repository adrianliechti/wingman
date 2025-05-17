package llm

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"
)

type Client struct {
	completer provider.Completer
}

func New(completer provider.Completer) (*Client, error) {
	c := &Client{
		completer: completer,
	}

	return c, nil
}

func (c *Client) Translate(ctx context.Context, content string, options *translator.TranslateOptions) (*translator.Translation, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	messages := []provider.Message{
		provider.SystemMessage("Act as a translator. Translate the following text to `" + options.Language + "`. Only return the translation, no other text."),
		provider.UserMessage(content),
	}

	completion, err := c.completer.Complete(ctx, messages, nil)

	if err != nil {
		return nil, err
	}

	result := &translator.Translation{
		Text: completion.Message.Text(),
	}

	return result, nil
}
