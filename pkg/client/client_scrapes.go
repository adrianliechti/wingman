package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type ScrapeService struct {
	Options []RequestOption
}

func NewScrapeService(opts ...RequestOption) ScrapeService {
	return ScrapeService{
		Options: opts,
	}
}

type Scrape struct {
	Text string `json:"text"`

	Content     []byte
	ContentType string
}

type ScrapeRequest struct {
	URL   string
	Model string

	Schema *Schema
}

func (r *ScrapeService) New(ctx context.Context, input ScrapeRequest, opts ...RequestOption) (*Scrape, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	data := url.Values{}
	data.Set("url", input.URL)

	if input.Model != "" {
		data.Set("model", input.Model)
	}

	if input.Schema != nil {
		properties, err := json.Marshal(input.Schema.Properties)

		if err != nil {
			return nil, err
		}

		data.Set("schema", string(properties))
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint(c.URL, "/v1/extract"), strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	content, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	result := &Scrape{
		Text:        string(content),
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
	}

	return result, nil
}
