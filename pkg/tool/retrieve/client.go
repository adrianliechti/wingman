package retrieve

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
	tools := []tool.Tool{
		{
			Name:        "retrieve_documents",
			Description: "Query the knowledge base to find relevant documents to answer questions",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language query input. The query input should be clear and standalone",
					},
				},

				"required": []string{"query"},
			},
		},
	}

	return tools, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "retrieve_documents" {
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
			Title:   r.Title,
			Source:  r.Source,
			Content: r.Content,
		}

		results = append(results, result)
	}

	return results, nil
}
