package replicate

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/replicate/replicate-go"
)

type Client struct {
	*Config
	client *replicate.Client
}

type PredictionInput = replicate.PredictionInput
type PredictionOutput = replicate.PredictionOutput

type File = replicate.File
type FileOutput = replicate.FileOutput

func New(model string, options ...Option) (*Client, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	client, err := replicate.NewClient(cfg.Options()...)

	if err != nil {
		return nil, err
	}

	return &Client{
		Config: cfg,
		client: client,
	}, nil
}

func (c *Client) Run(ctx context.Context, input PredictionInput) (PredictionOutput, error) {
	return c.client.RunWithOptions(ctx, c.model, input, nil, replicate.WithBlockUntilDone(), replicate.WithFileOutput())
}

func (c *Client) UploadFile(ctx context.Context, file provider.File) (*File, error) {
	return c.client.CreateFileFromBytes(ctx, file.Content, &replicate.CreateFileOptions{
		Filename:    file.Name,
		ContentType: file.ContentType,
	})
}

func (c *Client) DeleteFile(ctx context.Context, fileID string) error {
	return c.client.DeleteFile(ctx, fileID)
}
