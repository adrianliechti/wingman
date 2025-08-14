package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/retriever"
)

var _ retriever.Provider = &Client{}

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

func (c *Client) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	if options == nil {
		options = new(retriever.RetrieveOptions)
	}

	body, _ := json.Marshal(&SearchRequest{
		Query: query,

		Contents: SearchContents{
			Text:      true,
			LiveCrawl: LiveCrawlAlways,
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

	var results []retriever.Result

	for _, r := range data.Results {
		result := retriever.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: r.Text,
		}

		results = append(results, result)
	}

	return results, nil
}
