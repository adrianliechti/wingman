package search

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/tool"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	provider searcher.Provider

	limit int
}

func New(provider searcher.Provider, options ...Option) (*Client, error) {
	c := &Client{
		provider: provider,

		limit: 5,
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
						"description": "the text to search online for. search operator filters like site: are not supported",
					},

					"domains": map[string]any{
						"type":        "array",
						"description": "optional list of website domains to restrict the search to (e.g. wikipedia.org, github.com)",
						"items": map[string]any{
							"type": "string",
						},
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

	options := &searcher.SearchOptions{
		Limit: &c.limit,
	}

	if domains, ok := parameters["domains"].([]any); ok {
		for _, d := range domains {
			if domain, ok := d.(string); ok {
				options.Include = append(options.Include, domain)
			}
		}
	}

	data, err := c.provider.Search(ctx, query, options)

	if err != nil {
		return nil, err
	}

	results := []Result{}

	for _, r := range data {
		result := Result{
			Title:   r.Title,
			Source:  r.Source,
			Content: r.Content,
		}

		results = append(results, result)
	}

	return results, nil
}
