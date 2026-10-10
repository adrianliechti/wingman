package search

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/tool"
)

const ToolName = "web_search"

const (
	maxQueries  = 8
	concurrency = 4
)

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	provider searcher.Provider

	limit           int
	maxSnippetChars int

	now func() time.Time
}

func New(p searcher.Provider, options ...Option) (*Client, error) {
	if p == nil {
		return nil, errors.New("search: missing searcher provider")
	}

	c := &Client{
		provider:        p,
		limit:           5,
		maxSnippetChars: 400,

		now: time.Now,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	capabilities := c.provider.Capabilities()
	props := map[string]any{
		"query": map[string]any{
			"type":        "string",
			"minLength":   1,
			"description": "One natural-language search query. Do not include site: or other search operators; use allowed_domains/blocked_domains instead.",
		},
		"queries": map[string]any{
			"type":        "array",
			"minItems":    1,
			"maxItems":    maxQueries,
			"description": fmt.Sprintf("Up to %d independent queries to run in parallel instead of query; the other filters apply to all of them. Batch distinct lookups, not near-identical variants.", maxQueries),
			"items":       map[string]any{"type": "string", "minLength": 1},
		},
		"recency": map[string]any{
			"type":        "string",
			"enum":        []string{"day", "week", "month", "year"},
			"description": "Only results published within this window; a shortcut for since. Use for news, prices, releases and other time-sensitive facts.",
		},
		"max_results": map[string]any{
			"type":        "integer",
			"minimum":     1,
			"maximum":     10,
			"description": fmt.Sprintf("Number of results to return (default %d). Use 8-10 for broad discovery, 2-3 for a quick fact check.", c.limit),
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
		"since": map[string]any{
			"type":        "string",
			"description": "Optional earliest publication date (YYYY-MM-DD). Use for news, prices, releases and other time-sensitive facts; omit for stable facts.",
		},
		"until": map[string]any{
			"type":        "string",
			"description": "Optional latest publication date (YYYY-MM-DD), including that whole UTC day.",
		},
	}
	if !capabilities.DateFilters {
		delete(props, "recency")
		delete(props, "since")
		delete(props, "until")
	}

	if cats := capabilities.Categories; len(cats) > 0 {
		var b strings.Builder
		var names []string
		b.WriteString("Optional provider-supported category; omit for general web searches. Put other topic preferences in query. Available categories and restrictions:")
		for _, cat := range cats {
			names = append(names, cat.Name)
			if cat.Description != "" {
				fmt.Fprintf(&b, "\n- %s: %s", cat.Name, cat.Description)
			} else {
				fmt.Fprintf(&b, "\n- %s", cat.Name)
			}
		}
		props["category"] = map[string]any{
			"type":        "string",
			"enum":        names,
			"description": b.String(),
		}
	}

	return []tool.Tool{
		{
			Name:        ToolName,
			Description: "Search the public web for ranked sources, excerpts and publication dates when available. Pass query or queries for independent lookups with the same filters. Repeated excerpts are deduplicated; distinct evidence from the same URL is retained. Answer from excerpts when sufficient; fetch for missing facts, context or quotations.",

			Parameters: map[string]any{
				"type":       "object",
				"properties": props,
				"anyOf": []map[string]any{
					{"required": []string{"query"}},
					{"required": []string{"queries"}},
				},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != ToolName {
		return nil, tool.ErrInvalidTool
	}

	queries, err := collectQueries(parameters)
	if err != nil {
		return nil, err
	}

	limit, err := tool.IntegerParameter(parameters, "max_results", c.limit, 1, 10)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	options := &searcher.SearchOptions{
		Limit: &limit,
	}

	category, err := tool.StringParameter(parameters, "category")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	options.Category = strings.ToLower(strings.TrimSpace(category))
	location, err := tool.StringParameter(parameters, "location")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	options.Location = strings.ToUpper(strings.TrimSpace(location))
	if options.Include, err = tool.StringsParameter(parameters, "allowed_domains"); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if options.Exclude, err = tool.StringsParameter(parameters, "blocked_domains"); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	if options.Since, err = parseDate(parameters["since"], false); err != nil {
		return nil, fmt.Errorf("search: invalid since: %w", err)
	}

	if options.Until, err = parseDate(parameters["until"], true); err != nil {
		return nil, fmt.Errorf("search: invalid until: %w", err)
	}

	recency, err := tool.StringParameter(parameters, "recency")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if strings.TrimSpace(recency) != "" {
		if options.Since != nil {
			return nil, errors.New("search: use recency or since, not both")
		}
		since, err := recencySince(recency, c.now())
		if err != nil {
			return nil, err
		}
		options.Since = &since
	}
	if !c.provider.Capabilities().DateFilters && (options.Since != nil || options.Until != nil) {
		return nil, errors.New("search: this provider does not support publication date filters")
	}

	if options.Since != nil && options.Until != nil && !options.Since.Before(*options.Until) {
		return nil, errors.New("search: since must be before until")
	}

	if len(queries) == 1 {
		hits, err := c.provider.Search(ctx, queries[0], options)
		if err != nil {
			return nil, err
		}
		return formatResults(hits, c.maxSnippetChars), nil
	}

	// Independent lookups run concurrently; each query keeps its own result or error.
	type outcome struct {
		hits []searcher.Result
		err  error
	}
	outcomes := make([]outcome, len(queries))
	jobs := make(chan int, len(queries))
	for i := range queries {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(concurrency, len(queries)) {
		wg.Go(func() {
			for i := range jobs {
				hits, err := c.provider.Search(ctx, queries[i], options)
				outcomes[i] = outcome{hits: hits, err: err}
			}
		})
	}
	wg.Wait()

	failed := 0
	// A source can expose different passages for different queries. Deduplicate
	// identical evidence, while retaining new text and publication dates.
	type evidence struct{ source, title, content, published string }
	seen := map[evidence]bool{}
	var b strings.Builder
	for i, query := range queries {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## Query: %s\n", query)
		if outcomes[i].err != nil {
			failed++
			fmt.Fprintf(&b, "Error: %s\n", outcomes[i].err.Error())
			continue
		}
		var fresh []searcher.Result
		omitted := 0
		for _, hit := range outcomes[i].hits {
			key := evidence{source: canonicalURL(hit.Source), title: hit.Title, content: hit.Content}
			if hit.Timestamp != nil {
				key.published = hit.Timestamp.UTC().Format(time.RFC3339Nano)
			}
			if key.source != "" && seen[key] {
				omitted++
				continue
			}
			if key.source != "" {
				seen[key] = true
			}
			fresh = append(fresh, hit)
		}
		b.WriteString(formatResults(fresh, c.maxSnippetChars))
		if omitted > 0 {
			fmt.Fprintf(&b, "%d result(s) already listed above omitted.\n", omitted)
		}
	}
	if failed == len(queries) {
		return nil, outcomes[0].err
	}
	return b.String(), nil
}

// collectQueries accepts query, queries or both, trimmed and deduplicated.
func collectQueries(parameters map[string]any) ([]string, error) {
	query, err := tool.StringParameter(parameters, "query")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	batch, err := tool.StringsParameter(parameters, "queries")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var queries []string
	seen := map[string]bool{}
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		queries = append(queries, q)
	}
	add(query)
	for _, q := range batch {
		add(q)
	}
	if len(queries) == 0 {
		return nil, errors.New("search: missing query parameter")
	}
	if len(queries) > maxQueries {
		return nil, fmt.Errorf("search: use at most %d queries per call", maxQueries)
	}
	return queries, nil
}

func canonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSuffix(raw, "/"))
	}
	u.Fragment = ""
	u.Host = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	return strings.TrimSuffix(u.String(), "/")
}

// Result implements tool.Resulter so the agent chain sees the same markdown
// the MCP server emits, instead of a JSON-quoted blob.
func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return tool.TextResult(text)
}

func formatResults(hits []searcher.Result, maxChars int) string {
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
		fmt.Fprintf(&b, "%d. [%s](%s)", i+1, title, h.Source)
		if h.Timestamp != nil {
			fmt.Fprintf(&b, " — %s", h.Timestamp.Format("2006-01-02"))
		}
		b.WriteString("\n")
		if s := snippet(h.Content, maxChars); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return b.String()
}

func snippet(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 {
		return text
	}

	runes := []rune(text)
	if len(runes) <= max {
		return text
	}

	cut := string(runes[:max])
	if i := strings.LastIndexAny(cut, " \n\t"); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

func parseDate(v any, upper bool) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("date must be a string")
	}
	return searcher.ParseDateBound(s, upper)
}
