package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	if len(data.Results) == 0 {
		for _, s := range data.Statuses {
			if s.Status != "success" && s.Error != nil {
				return nil, fmt.Errorf("exa: %s (%s)", s.Error.Tag, url)
			}
		}
		return nil, fmt.Errorf("exa: no content returned for %s", url)
	}

	result := &scraper.Document{
		Text: data.Results[0].Text,
	}

	return result, nil
}
