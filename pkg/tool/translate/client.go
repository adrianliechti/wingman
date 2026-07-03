package translate

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/translator"
)

const ToolName = "translate"

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	provider translator.Provider
}

func New(provider translator.Provider, options ...Option) (*Client, error) {
	if provider == nil {
		return nil, errors.New("translate: missing translator provider")
	}

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
			Name:        ToolName,
			Description: "Translate text into a target language; the source language is detected automatically. Pass the text verbatim — do not pre-translate or summarize it. Returns only the translated text.",

			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The text to translate, verbatim.",
					},
					"lang": map[string]any{
						"type":        "string",
						"description": "Target language as an ISO 639-1 / BCP-47 code (e.g. 'de', 'en', 'fr', 'pt-BR'). The translation service returns an error for unsupported codes.",
					},
				},
				"required": []string{"text", "lang"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != ToolName {
		return nil, tool.ErrInvalidTool
	}

	text, _ := parameters["text"].(string)
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("translate: missing text parameter")
	}

	lang, _ := parameters["lang"].(string)
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return nil, errors.New("translate: missing lang parameter")
	}

	result, err := c.provider.Translate(ctx, translator.Input{Text: text}, &translator.TranslateOptions{Language: lang})
	if err != nil {
		return nil, err
	}

	return string(result.Content), nil
}

func (c *Client) Result(name string, value any) provider.ToolResult {
	if s, ok := value.(string); ok {
		return provider.ToolResult{Parts: []provider.Part{{Text: s}}}
	}
	return provider.ToolResult{}
}
