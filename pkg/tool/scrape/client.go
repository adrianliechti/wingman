package scrape

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/tool"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	scraper scraper.Provider
}

func New(scraper scraper.Provider, options ...Option) (*Client, error) {
	c := &Client{
		scraper: scraper,
	}

	for _, option := range options {
		option(c)
	}

	if c.scraper == nil {
		return nil, errors.New("missing scraper provider")
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "crawl_website",
			Description: "fetch and return the markdown content from a given URL, including website pages, YouTube video transcriptions, and similar sources",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "the URL of the website to crawl staring with http:// or https://",
					},
				},

				"required": []string{"url"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "crawl_website" {
		return nil, tool.ErrInvalidTool
	}

	url, ok := parameters["url"].(string)

	if !ok {
		return nil, errors.New("missing url parameter")
	}

	options := &scraper.ScrapeOptions{}

	document, err := c.scraper.Scrape(ctx, url, options)

	if err != nil {
		return nil, err
	}

	return string(document.Text), nil
}
