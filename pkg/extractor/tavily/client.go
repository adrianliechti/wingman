package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/provider"
)

var _ extractor.Provider = &Client{}

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

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*provider.File, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if input.URL == "" {
		return nil, extractor.ErrUnsupported
	}

	if options.Format != nil {
		if *options.Format != extractor.FormatText {
			return nil, extractor.ErrUnsupported
		}
	}

	u, _ := url.Parse("https://api.tavily.com/extract")

	body := map[string]any{
		"api_key":       c.token,
		"urls":          input.URL,
		"extract_depth": "advanced",
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

	var data extractResult

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	if len(data.Results) == 0 {
		return nil, errors.New("no results")
	}

	text := data.Results[0].Content

	result := &provider.File{
		Content:     []byte(text),
		ContentType: "text/plain",
	}

	return result, nil
}
