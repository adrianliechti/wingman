package research

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/tool"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	provider researcher.Provider
}

func New(provider researcher.Provider, options ...Option) (*Client, error) {
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
			Name:        "research_online",
			Description: "Deeply research the internet using an AI agent to thoroughly investigate and answer complex questions. Use this when you need comprehensive, well-researched information that goes beyond simple searches.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"instructions": map[string]any{
						"type":        "string",
						"description": "instructions for the research to be conducted",
					},
				},

				"required": []string{"instructions"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "research_online" {
		return nil, tool.ErrInvalidTool
	}

	instructions, ok := parameters["instructions"].(string)

	if !ok {
		return nil, errors.New("missing instructions parameter")
	}

	options := &researcher.ResearchOptions{}

	data, err := c.provider.Research(ctx, instructions, options)

	if err != nil {
		return nil, err
	}

	result := Result{
		Content: data.Content,
	}

	return result, nil
}
