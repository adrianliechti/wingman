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

	limit int
}

func New(p searcher.Provider, options ...Option) (*Client, error) {
	if p == nil {
		return nil, errors.New("search: missing searcher provider")
	}

	c := &Client{
		provider: p,
		limit:    5,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	props := map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "The natural-language search query. Do not include site: or other search operators.",
		},
		"location": map[string]any{
			"type":        "string",
			"description": "Optional two-letter ISO 3166-1 alpha-2 country code to bias results (e.g. \"US\", \"CH\", \"DE\").",
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
	}

	if cats := c.provider.Categories(); len(cats) > 0 {
		var b strings.Builder
		b.WriteString("Optional vertical to bias results toward. Any descriptive string is accepted as a hint; the entries below have improved quality:")
		for _, cat := range cats {
			if cat.Description != "" {
				fmt.Fprintf(&b, "\n- %s: %s", cat.Name, cat.Description)
			} else {
				fmt.Fprintf(&b, "\n- %s", cat.Name)
			}
		}
		props["category"] = map[string]any{
			"type":        "string",
			"description": b.String(),
		}
	}

	return []tool.Tool{
		{
			Name:        ToolName,
			Description: "Search the public web and return a list of sources (URL, title, snippet) the assistant can cite. Use for current events, named entities, or anything that may have changed since training.",

			Parameters: map[string]any{
				"type":       "object",
				"properties": props,
				"required":   []string{"query"},
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

	if cat, _ := parameters["category"].(string); cat != "" {
		options.Category = strings.ToLower(strings.TrimSpace(cat))
	}

	if loc, _ := parameters["location"].(string); loc != "" {
		options.Location = strings.ToUpper(strings.TrimSpace(loc))
	}

	options.Include = collectStrings(parameters["allowed_domains"])
	options.Exclude = collectStrings(parameters["blocked_domains"])

	hits, err := c.provider.Search(ctx, query, options)
	if err != nil {
		return nil, err
	}

	return formatResults(hits), nil
}

// Result implements tool.Resulter so the agent chain sees the same markdown
// the MCP server emits, instead of a JSON-quoted blob.
func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return provider.ToolResult{
		Parts: []provider.Part{{Text: text}},
	}
}

func formatResults(hits []searcher.Result) string {
	if len(hits) == 0 {
		return "No results."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s):\n\n", len(hits))
	for i, h := range hits {
		title := h.Title
		if title == "" {
			title = h.Source
		}
		fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, title, h.Source)
		if s := snippet(h.Content, 240); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return b.String()
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
