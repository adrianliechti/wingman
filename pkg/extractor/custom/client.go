package custom

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	_ extractor.Provider = (*Client)(nil)
)

type Client struct {
	client       ExtractorClient
	capabilities *extractor.Capabilities
}

func (c *Client) Capabilities() extractor.Capabilities {
	if c.capabilities == nil {
		return extractor.Capabilities{UnknownFormats: true}
	}
	result := *c.capabilities
	result.MediaTypes = slices.Clone(result.MediaTypes)
	result.Extensions = slices.Clone(result.Extensions)
	return result
}

func New(url string, options ...Option) (*Client, error) {
	if !strings.HasPrefix(url, "grpc://") || strings.TrimSpace(strings.TrimPrefix(url, "grpc://")) == "" {
		return nil, errors.New("invalid url")
	}
	connection, err := grpc.NewClient(strings.TrimPrefix(url, "grpc://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)),
	)
	if err != nil {
		return nil, err
	}

	c := &Client{
		client: NewExtractorClient(connection),
	}

	for _, option := range options {
		option(c)
	}

	if err := c.loadCapabilities(); err != nil {
		connection.Close()
		return nil, fmt.Errorf("custom extractor capabilities: %w", err)
	}

	return c, nil
}

func (c *Client) loadCapabilities() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := c.client.Capabilities(ctx, &CapabilitiesRequest{})
	if status.Code(err) == codes.Unimplemented {
		return nil
	}
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("empty capabilities response")
	}
	c.capabilities = &extractor.Capabilities{
		MediaTypes:     slices.Clone(response.MediaTypes),
		Extensions:     slices.Clone(response.Extensions),
		UnknownFormats: response.UnknownFormats,
	}
	return nil
}

func (c *Client) Extract(ctx context.Context, file extractor.File, options *extractor.ExtractOptions) (*extractor.Document, error) {
	if !c.Capabilities().MaySupport(file) {
		return nil, extractor.ErrUnsupported
	}
	if options == nil {
		options = new(extractor.ExtractOptions)
	}

	req := &ExtractRequest{
		File: &File{
			Name: file.Name,

			Content:     file.Content,
			ContentType: file.ContentType,
		},
	}

	resp, err := c.client.Extract(ctx, req)

	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("custom extractor: empty response")
	}

	return &extractor.Document{
		Text: resp.Text,
	}, nil
}
