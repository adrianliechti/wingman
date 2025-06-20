package jina

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

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*provider.File, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	if input.URL == "" {
		return nil, extractor.ErrUnsupported
	}

	body := map[string]any{
		"url": input.URL,
	}

	format := "text"
	contentType := "text/plain"

	if options.Format != nil {
		switch *options.Format {
		case extractor.FormatText:
			format = "text"
			contentType = "text/plain"

		case extractor.FormatImage:
			format = "pageshot"
			contentType = "image/png"

		case extractor.FormatPDF:
			format = "pdf"
			contentType = "application/pdf"

		default:
			return nil, extractor.ErrUnsupported
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.url, jsonReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Return-Format", format)

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

	return &provider.File{
		Content:     data,
		ContentType: contentType,
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
