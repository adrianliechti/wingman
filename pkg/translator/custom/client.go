package custom

import (
	"context"
	"errors"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/translator"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	_ translator.Provider = (*Client)(nil)
)

type Client struct {
	url    string
	client TranslatorClient
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

	c.client = NewTranslatorClient(client)

	return c, nil
}

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*provider.File, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	req := &TranslateRequest{
		Language: options.Language,
	}

	if input.Text != "" {
		req.Text = input.Text
	}

	if input.File != nil {
		req.File = &File{
			Name: input.File.Name,

			Content:     input.File.Content,
			ContentType: input.File.ContentType,
		}
	}

	resp, err := c.client.Translate(ctx, req)

	if err != nil {
		return nil, err
	}

	return &provider.File{
		Content:     resp.Content,
		ContentType: resp.ContentType,
	}, nil
}
