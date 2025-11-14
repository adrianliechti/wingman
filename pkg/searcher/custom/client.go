package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/searcher"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ searcher.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client SearcherClient
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

	c.client = NewSearcherClient(client)

	return c, nil
}

func (c *Client) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	if options == nil {
		options = new(searcher.SearchOptions)
	}

	req := &SearchRequest{
		Query: query,
	}

	if options.Limit != nil {
		val := int32(*options.Limit)
		req.Limit = &val
	}

	resp, err := c.client.Search(ctx, req)

	if err != nil {
		return nil, err
	}

	results := []searcher.Result{}

	for _, r := range resp.Results {
		results = append(results, searcher.Result{
			Source: r.Source,

			Title:   r.Title,
			Content: r.Content,

			Metadata: r.Metadata,
		})
	}

	return results, nil
}
