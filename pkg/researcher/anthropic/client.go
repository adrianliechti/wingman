package anthropic

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/anthropics/anthropic-sdk-go"
)

var _ researcher.Provider = &Client{}

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

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = new(researcher.ResearchOptions)
	}

	model := c.model

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 1024,

		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(instructions)),
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

	var content string

	for _, c := range message.Content {
		if c.Type == "text" && len(c.Citations) > 0 {
			content += c.Text
		}
	}

	result := &researcher.Result{
		Content: content,
	}

	return result, nil
}
