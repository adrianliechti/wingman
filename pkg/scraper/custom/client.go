package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/scraper"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ scraper.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client ScraperClient
}

func New(url string, options ...Option) (*Client, error) {
	if url == "" || !strings.HasPrefix(url, "grpc://") {
		return nil, errors.New("invalid url")
	}

	c := &Client{
		url: url,
	}

	for _, option := range options {
		option(c)
	}

	client, err := grpc.NewClient(strings.TrimPrefix(c.url, "grpc://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)), // 100MB max receive message size
	)

	if err != nil {
		return nil, err
	}

	c.client = NewScraperClient(client)

	return c, nil
}

func (c *Client) Scrape(ctx context.Context, url string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	if options == nil {
		options = new(scraper.ScrapeOptions)
	}

	req := &ScrapeRequest{
		Url: url,
	}

	resp, err := c.client.Scrape(ctx, req)

	if err != nil {
		return nil, err
	}

	return &scraper.Document{
		Text: resp.Text,
	}, nil
}
