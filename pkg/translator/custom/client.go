package custom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/translator"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	_ translator.Provider = (*Client)(nil)
)

type Client struct {
	client       TranslatorClient
	capabilities *translator.Capabilities
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
		client: NewTranslatorClient(connection),
	}

	for _, option := range options {
		option(c)
	}
	if err := c.loadCapabilities(); err != nil {
		connection.Close()
		return nil, fmt.Errorf("custom translator capabilities: %w", err)
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
	textToText, err := support(response.TextToText)
	if err != nil {
		return err
	}
	fileToText, err := support(response.FileToText)
	if err != nil {
		return err
	}
	fileToDocument, err := support(response.FileToDocument)
	if err != nil {
		return err
	}
	c.capabilities = &translator.Capabilities{
		TextToText: textToText, FileToText: fileToText, FileToDocument: fileToDocument,
	}
	return nil
}

func support(value Support) (translator.Support, error) {
	switch value {
	case Support_SUPPORT_UNKNOWN:
		return translator.Unknown, nil
	case Support_SUPPORT_SUPPORTED:
		return translator.Supported, nil
	case Support_SUPPORT_UNSUPPORTED:
		return translator.Unsupported, nil
	default:
		return translator.Unsupported, fmt.Errorf("invalid support value %d", value)
	}
}

func (c *Client) Capabilities() translator.Capabilities {
	if c.capabilities != nil {
		return *c.capabilities
	}
	// Legacy services do not report their supported modes.
	return translator.Capabilities{TextToText: translator.Unknown, FileToText: translator.Unknown, FileToDocument: translator.Unknown}
}

func (c *Client) Translate(ctx context.Context, input translator.Input, options *translator.TranslateOptions) (*translator.File, error) {
	capabilities := c.Capabilities()
	if input.File != nil {
		if !capabilities.FileToText.MaySupport() && !capabilities.FileToDocument.MaySupport() {
			return nil, translator.ErrUnsupported
		}
	} else if !capabilities.TextToText.MaySupport() {
		return nil, translator.ErrUnsupported
	}
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
	if resp == nil {
		return nil, errors.New("custom translator: empty response")
	}

	return &translator.File{
		Name:        resp.Name,
		Content:     resp.Content,
		ContentType: resp.ContentType,
	}, nil
}
