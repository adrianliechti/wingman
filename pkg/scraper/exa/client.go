package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

	body, _ := json.Marshal(&ContentsRequest{
		URLs: []string{url},

		Text: true,

		LiveCrawl: LiveCrawlPreferred,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.exa.ai/contents", bytes.NewBuffer(body))
	req.Header.Set("x-api-key", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(body))
	}

	var data ContentsResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	text := data.Results[0].Text

	result := &scraper.Document{
		Text: text,
	}

	return result, nil
}
