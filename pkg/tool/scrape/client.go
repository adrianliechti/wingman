package scrape

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

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
			Description: "Fetch the content of a public web page (HTML/text/PDF) and return its text so the assistant can quote and cite it.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The absolute URL to fetch. Must include scheme (http or https).",
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

	raw, _ := parameters["url"].(string)
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

	doc, err := c.scraper.Scrape(ctx, raw, &scraper.ScrapeOptions{})
	if err != nil {
		return nil, err
	}

	text := doc.Text
	if c.maxChars > 0 && len(text) > c.maxChars {
		text = text[:c.maxChars]
	}

	return formatDocument(raw, text), nil
}

// Result implements tool.Resulter so the agent chain sees the same markdown
// the MCP server emits.
func (c *Client) Result(name string, value any) provider.ToolResult {
	text, _ := value.(string)
	return provider.ToolResult{
		Parts: []provider.Part{{Text: text}},
	}
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
