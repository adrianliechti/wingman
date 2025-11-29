package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/scraper"
)

var _ scraper.Provider = &Client{}

type Client struct {
	token  string
	client *http.Client
}

func New(token string, options ...Option) (*Client, error) {
	c := &Client{
		token:  token,
		client: http.DefaultClient,
	}

	for _, option := range options {
		option(c)
	}

	if c.token == "" {
		return nil, errors.New("invalid token")
	}

	return c, nil
}

func (c *Client) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if options == nil {
		options = new(scraper.ScrapeOptions)
	}

	body := map[string]any{
		"api_key":       c.token,
		"urls":          url,
		"extract_depth": "advanced",
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/extract", jsonReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var data extractResult

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	if len(data.Results) == 0 {
		return nil, errors.New("no results")
	}

	text := data.Results[0].Content

	result := &scraper.Document{
		Text: text,
	}

	return result, nil
}
