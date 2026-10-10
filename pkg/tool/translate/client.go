package translate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"

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
	if !provider.Capabilities().TextToText.MaySupport() {
		return nil, fmt.Errorf("translate: provider does not support text translation: %w", translator.ErrUnsupported)
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
						"description": "Target language as an ISO 639-1 / BCP-47 code (e.g. 'de', 'en', 'fr', 'pt-BR'). Malformed codes are rejected.",
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

	text, err := tool.StringParameter(parameters, "text")
	if err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("translate: missing text parameter")
	}

	lang, err := tool.StringParameter(parameters, "lang")
	if err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return nil, errors.New("translate: missing lang parameter")
	}
	if _, err := language.Parse(lang); err != nil {
		return nil, fmt.Errorf("translate: invalid language code %q", lang)
	}

	result, err := c.provider.Translate(ctx, translator.Input{Text: text}, &translator.TranslateOptions{Language: lang})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("translate: empty response")
	}

	return string(result.Content), nil
}

func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return tool.TextResult(text)
}
