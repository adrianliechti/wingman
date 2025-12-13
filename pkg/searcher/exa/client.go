package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

var _ searcher.Provider = &Client{}

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

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	if options == nil {
		options = new(searcher.SearchOptions)
	}

	body, _ := json.Marshal(&SearchRequest{
		Query: query,

		NumResults: options.Limit,

		IncludeDomains: options.Domains,

		Contents: SearchContents{
			Text:      true,
			LiveCrawl: LiveCrawlPreferred,
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.exa.ai/search", bytes.NewReader(body))
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

	var data SearchResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var results []searcher.Result

	for _, r := range data.Results {
		result := searcher.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: r.Text,
		}

		results = append(results, result)
	}

	return results, nil
}
