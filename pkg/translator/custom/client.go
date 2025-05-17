package custom

import (
	"context"
	"errors"
	"strings"

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
	)

	if err != nil {
		return nil, err
	}

	c.client = NewTranslatorClient(client)

	return c, nil
}

func (c *Client) Translate(ctx context.Context, text string, options *translator.TranslateOptions) (*translator.Translation, error) {
	if options == nil {
		options = new(translator.TranslateOptions)
	}

	resp, err := c.client.Translate(ctx, &TranslateRequest{
		Text: text,

		Language: options.Language,
	})

	if err != nil {
		return nil, err
	}

	return &translator.Translation{
		Text: resp.Text,
	}, nil
}
