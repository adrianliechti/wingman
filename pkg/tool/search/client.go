package search

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/index"
	"github.com/adrianliechti/wingman/pkg/tool"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	provider index.Provider
}

func New(provider index.Provider, options ...Option) (*Client, error) {
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
			Name:        "search_online",
			Description: "Search online if the requested information cannot be found in the language model or the information could be present in a time after the language model was trained",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "the text to search online for",
					},
				},

				"required": []string{"query"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "search_online" {
		return nil, tool.ErrInvalidTool
	}

	query, ok := parameters["query"].(string)

	if !ok {
		return nil, errors.New("missing query parameter")
	}

	options := &index.QueryOptions{}

	data, err := c.provider.Query(ctx, query, options)

	if err != nil {
		return nil, err
	}

	results := []Result{}

	for _, r := range data {
		result := Result{
			URL: r.Source,

			Title:   r.Title,
			Content: r.Content,
		}

		results = append(results, result)
	}

	return results, nil
}
