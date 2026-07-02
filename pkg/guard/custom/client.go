package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/guard"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ guard.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client GuardClient
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

	c.client = NewGuardClient(client)

	return c, nil
}

func (c *Client) Check(ctx context.Context, text string, options *guard.CheckOptions) (*guard.Result, error) {
	if options == nil {
		options = new(guard.CheckOptions)
	}

	req := &CheckRequest{
		Text: text,
	}

	resp, err := c.client.Check(ctx, req)

	if err != nil {
		return nil, err
	}

	result := &guard.Result{
		Flagged: resp.Flagged,
	}

	for _, c := range resp.Categories {
		result.Categories = append(result.Categories, guard.Category{
			Name:  c.Name,
			Score: c.Score,
		})
	}

	return result, nil
}
