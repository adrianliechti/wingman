package translate

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/translator"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	provider translator.Provider
}

func New(provider translator.Provider, options ...Option) (*Client, error) {
	c := &Client{
		provider: provider,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "translate_text",
			Description: "Translate text to the given language.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The text to translate",
					},

					"lang": map[string]any{
						"type":        "string",
						"description": "The target language code",
						"enum": []string{
							"de",
							"en",
							"fr",
							"it",
						},
					},
				},

				"required": []string{"text", "lang"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "translate_text" {
		return nil, tool.ErrInvalidTool
	}

	text, ok := parameters["text"].(string)

	if !ok {
		return nil, errors.New("missing text parameter")
	}

	lang, ok := parameters["lang"].(string)

	if !ok {
		lang = "en"
	}

	options := &translator.TranslateOptions{
		Language: lang,
	}

	data, err := c.provider.Translate(ctx, text, options)

	if err != nil {
		return nil, err
	}

	return &Result{
		Language: lang,
		Text:     data.Text,
	}, nil
}
