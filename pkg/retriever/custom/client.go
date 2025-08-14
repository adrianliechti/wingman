package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/retriever"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ retriever.Provider = (*Client)(nil)
)

type Client struct {
	url string

	client RetrieverClient
}

type Option func(*Client)

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

	url = strings.TrimPrefix(c.url, "grpc://")

	client, err := grpc.NewClient(url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)), // 100MB max receive message size
	)

	if err != nil {
		return nil, err
	}

	c.client = NewRetrieverClient(client)

	return c, nil
}

func (c *Client) Retrieve(ctx context.Context, query string, options *retriever.RetrieveOptions) ([]retriever.Result, error) {
	if options == nil {
		options = new(retriever.RetrieveOptions)
	}

	var limit *int32

	if options.Limit != nil {
		val := int32(*options.Limit)
		limit = &val
	}

	data, err := c.client.Retrieve(ctx, &RetrieveRequest{
		Query: query,

		Limit: limit,
	})

	if err != nil {
		return nil, err
	}

	return convertResults(data.Results), nil
}

func convertResults(s []*Result) []retriever.Result {
	var result []retriever.Result

	for _, r := range s {
		result = append(result, convertResult(r))
	}

	return result
}

func convertResult(r *Result) retriever.Result {
	return retriever.Result{
		ID: r.Id,

		Source: r.Source,

		Score:   r.Score,
		Title:   r.Title,
		Content: r.Content,

		Metadata: r.Metadata,
	}
}
