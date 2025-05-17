package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/summarizer"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ summarizer.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client SummarizerClient
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
	)

	if err != nil {
		return nil, err
	}

	c.client = NewSummarizerClient(client)

	return c, nil
}

func (c *Client) Summarize(ctx context.Context, text string, options *summarizer.SummarizerOptions) (*summarizer.Summary, error) {
	if options == nil {
		options = new(summarizer.SummarizerOptions)
	}

	resp, err := c.client.Summarize(ctx, &SummarizeRequest{
		Text: text,
	})

	if err != nil {
		return nil, err
	}

	return &summarizer.Summary{
		Text: resp.Text,
	}, nil
}
