package anthropic

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/retriever"
	"github.com/anthropics/anthropic-sdk-go"
)

var _ retriever.Provider = &Client{}

type Client struct {
	*Config
	messages anthropic.MessageService
}

func New(token string, options ...Option) (*Client, error) {
	cfg := &Config{
		token: token,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Client{
		Config:   cfg,
		messages: anthropic.NewMessageService(cfg.Options()...),
	}, nil
}

func (c *Client) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	if options == nil {
		options = new(retriever.RetrieveOptions)
	}

	model := c.model

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 1024,

		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(query)),
		},

		Tools: []anthropic.ToolUnionParam{
			{
				OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{
					MaxUses: anthropic.Int(5),
				},
			},
		},
	}

	message, err := c.messages.New(ctx, body)

	if err != nil {
		return nil, err
	}

	var result []retriever.Result

	for _, c := range message.Content {
		for _, c := range c.Citations {
			result = append(result, retriever.Result{
				Source: c.URL,

				Title:   c.Title,
				Content: c.CitedText,
			})
		}
	}

	return result, nil
}
