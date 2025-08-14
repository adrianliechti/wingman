package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

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

	u, _ := url.Parse("https://api.tavily.com/search")

	body := map[string]any{
		"api_key":      c.token,
		"query":        query,
		"search_depth": "advanced",
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", u.String(), jsonReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, convertError(resp)
	}

	var data searchResult

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var results []retriever.Result

	for _, r := range data.Results {
		result := retriever.Result{
			Source: r.URL,

			Title:   r.Title,
			Content: r.Content,
		}

		results = append(results, result)
	}

	return results, nil
}
