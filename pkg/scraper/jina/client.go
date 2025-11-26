package jina

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
	client *http.Client

	url   string
	token string
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" {
		url = "https://r.jina.ai/"
	}

	c := &Client{
		client: http.DefaultClient,

		url: url,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if options == nil {
		options = new(scraper.ScrapeOptions)
	}

	body := map[string]any{
		"url": url,
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.url, jsonReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Return-Format", "text")

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &scraper.Document{
		Text: string(data),
	}, nil
}

func convertError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	if len(data) == 0 {
		return errors.New(http.StatusText(resp.StatusCode))
	}

	return errors.New(string(data))
}

func jsonReader(v any) io.Reader {
	b := new(bytes.Buffer)

	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
	return b
}
