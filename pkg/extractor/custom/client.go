package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/extractor"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ extractor.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client ExtractorClient
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

	c.client = NewExtractorClient(client)

	return c, nil
}

func (c *Client) Extract(ctx context.Context, input extractor.Input, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	req := &ExtractRequest{
		Url: input.URL,
	}

	if input.File != nil {
		req.File = &File{
			Name: input.File.Name,

			Content:     input.File.Content,
			ContentType: input.File.ContentType,
		}
	}

	resp, err := c.client.Extract(ctx, req)

	if err != nil {
		return nil, err
	}

	return &extractor.Document{
		Content:     resp.Content,
		ContentType: resp.ContentType,
	}, nil
}
