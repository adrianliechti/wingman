package scrape

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/tool"
)

const ToolName = "web_fetch"

const defaultMaxChars = 32 * 1024

var ErrURLNotAllowed = errors.New("scrape: url not allowed")

var (
	_ tool.Provider = (*Client)(nil)
	_ tool.Resulter = (*Client)(nil)
)

type Client struct {
	scraper scraper.Provider

	maxChars int

	allowedDomains []string
	blockedDomains []string
}

func New(scraper scraper.Provider, options ...Option) (*Client, error) {
	c := &Client{
		scraper:  scraper,
		maxChars: defaultMaxChars,
	}

	for _, option := range options {
		option(c)
	}

	if c.scraper == nil {
		return nil, errors.New("scrape: missing scraper provider")
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        ToolName,
			Description: "Read an http(s) URL through the configured scraper and return extracted source text for citations and quotations. Use query to select relevant passages, or start_index to continue reading. Omission notices describe which text was left out; omitted text cannot establish absence.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The absolute URL to fetch. Must include scheme (http or https).",
					},
					"start_index": map[string]any{
						"type":        "integer",
						"minimum":     0,
						"description": "Character offset in the original page for sequential reading. A positive offset overrides query; 0 or omitted allows query-focused reading.",
					},
					"query": map[string]any{
						"maxLength":   500,
						"type":        "string",
						"description": "Keywords for the facts needed. Locally selects verbatim passages from long pages, with original character offsets; no generated summary. Omit to read sequentially.",
					},
					"max_chars": map[string]any{
						"type": "integer", "minimum": 1, "maximum": c.maxChars,
						"description": "Maximum returned page characters. Omit for the configured default.",
					},
				},

				"required": []string{"url"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != ToolName {
		return nil, tool.ErrInvalidTool
	}

	raw, err := tool.StringParameter(parameters, "url")
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("scrape: missing url parameter")
	}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("scrape: invalid url %q", raw)
	}

	if !c.allowed(parsed.Hostname()) {
		return nil, ErrURLNotAllowed
	}

	start, err := tool.IntegerParameter(parameters, "start_index", 0, 0, int(^uint(0)>>1))
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	maxChars, err := tool.IntegerParameter(parameters, "max_chars", c.maxChars, 1, c.maxChars)
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}

	query, err := tool.StringParameter(parameters, "query")
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	if utf8.RuneCountInString(query) > 500 {
		return nil, errors.New("scrape: query must be a string of at most 500 characters")
	}

	doc, err := c.scraper.Scrape(ctx, raw, &scraper.ScrapeOptions{})
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("scrape: empty document")
	}

	text := paginate(doc.Text, start, maxChars)
	if start == 0 && strings.TrimSpace(query) != "" {
		text = focusedText(doc.Text, query, maxChars)
	}

	return formatDocument(raw, text), nil
}

// Result implements tool.Resulter so the agent chain sees the same markdown
// the MCP server emits.
func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return tool.TextResult(text)
}

// paginate returns a window of at most max characters (runes) starting at
// start, so multi-byte characters are never split mid-rune. A truncated
// window ends with a notice telling the model how to continue. A
// non-positive max means no limit.
func paginate(text string, start, max int) string {
	runes := []rune(text)
	total := len(runes)

	if start >= total {
		return fmt.Sprintf("[start_index %d is beyond the end of the page (%d characters total)]", start, total)
	}
	if start > 0 {
		runes = runes[start:]
	}
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}

	end := start + max
	return string(runes[:max]) + fmt.Sprintf("\n\n[Truncated: showing characters %d-%d of %d. Fetch again with start_index=%d to continue.]", start, end, total, end)
}

func formatDocument(source, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source: %s\n\n", source)
	b.WriteString(text)
	return b.String()
}

func (c *Client) allowed(host string) bool {
	host = strings.ToLower(host)

	if len(c.allowedDomains) > 0 {
		var match bool
		for _, d := range c.allowedDomains {
			if matchDomain(host, d) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	for _, d := range c.blockedDomains {
		if matchDomain(host, d) {
			return false
		}
	}

	return true
}

func matchDomain(host, domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return false
	}
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}
