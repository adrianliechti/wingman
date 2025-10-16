package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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

	body, _ := json.Marshal(&ContentsRequest{
		URLs: []string{input.URL},

		Text:     true,

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

	content := data.Results[0].Text

	result := &provider.File{
		Content:     []byte(content),
		ContentType: "text/plain",
	}

	return result, nil
}
