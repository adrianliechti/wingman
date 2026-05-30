package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/tool"
)

const ToolName = "web_search"

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	provider searcher.Provider

	limit    int
	location string
}

func New(provider searcher.Provider, options ...Option) (*Client, error) {
	if provider == nil {
		return nil, errors.New("search: missing searcher provider")
	}

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
			Name:        ToolName,
			Description: "Search the public web for up-to-date information and return a list of sources the assistant can cite.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural-language search query. Do not include site: or other search operators.",
					},
					"allowed_domains": map[string]any{
						"type":        "array",
						"description": "Optional list of domains to restrict results to (e.g. \"go.dev\", \"wikipedia.org\").",
						"items":       map[string]any{"type": "string"},
					},
					"blocked_domains": map[string]any{
						"type":        "array",
						"description": "Optional list of domains to exclude from results.",
						"items":       map[string]any{"type": "string"},
					},
				},

				"required": []string{"query"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != ToolName {
		return nil, tool.ErrInvalidTool
	}

	query, _ := parameters["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search: missing query parameter")
	}

	options := &searcher.SearchOptions{
		Limit: &c.limit,
	}

	if c.location != "" {
		options.Location = c.location
	}

	options.Include = collectStrings(parameters["allowed_domains"])
	options.Exclude = collectStrings(parameters["blocked_domains"])

	hits, err := c.provider.Search(ctx, query, options)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(hits))
	for _, h := range hits {
		results = append(results, Result{
			URL:     h.Source,
			Title:   h.Title,
			Snippet: snippet(h.Content, 240),
			Content: h.Content,
		})
	}

	return results, nil
}

func (c *Client) Result(name string, value any) provider.ToolResult {
	results, _ := value.([]Result)

	var b strings.Builder
	if len(results) == 0 {
		b.WriteString("No results.")
	} else {
		fmt.Fprintf(&b, "Found %d result(s):\n\n", len(results))
		for i, r := range results {
			title := r.Title
			if title == "" {
				title = r.URL
			}
			fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, title, r.URL)
			if r.Snippet != "" {
				fmt.Fprintf(&b, "   %s\n", r.Snippet)
			}
		}
	}

	return provider.ToolResult{
		Parts: []provider.Part{{Text: b.String()}},
	}
}

func collectStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func snippet(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}

	cut := text[:max]
	if i := strings.LastIndexAny(cut, " \n\t"); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
