package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/researcher"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ researcher.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client ResearcherClient
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

	c.client = NewResearcherClient(client)

	return c, nil
}

func (c *Client) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) (*researcher.Result, error) {
	if options == nil {
		options = new(researcher.ResearchOptions)
	}

	req := &ResearchRequest{
		Instructions: instructions,
	}

	resp, err := c.client.Research(ctx, req)

	if err != nil {
		return nil, err
	}

	result := &researcher.Result{
		Content: resp.Content,
	}

	return result, nil
}
