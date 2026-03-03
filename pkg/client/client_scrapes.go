package client

import (
	"context"
	"errors"
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
}

type ScrapeRequest struct {
	URL string
}

func (r *ScrapeService) New(ctx context.Context, input ScrapeRequest, opts ...RequestOption) (*Scrape, error) {
	c := newRequestConfig(append(r.Options, opts...)...)

	data := url.Values{}
	data.Set("url", input.URL)

	req, _ := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/extract", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	result, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	return &Scrape{
		Text: string(result),
	}, nil
}
