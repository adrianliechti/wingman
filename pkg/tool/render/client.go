package render

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/google/uuid"
)

var _ tool.Provider = (*Client)(nil)

type Client struct {
	provider provider.Renderer
}

func New(provider provider.Renderer, options ...Option) (*Client, error) {
	c := &Client{
		provider: provider,
	}

	for _, option := range options {
		option(c)
	}

	return c, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "generate_image",
			Description: "Generate images based based on user-provided text prompt or edit an existing one in the context. Returns a URL to download the generated image.",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "detailed text description of the image to generate or edit. must be english.",
					},
				},

				"required": []string{"prompt"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "generate_image" {
		return nil, tool.ErrInvalidTool
	}

	prompt, ok := parameters["prompt"].(string)

	if !ok {
		return nil, errors.New("missing prompt parameter")
	}

	options := &provider.RenderOptions{}

	if files, ok := tool.FilesFromContext(ctx); ok {
		options.Images = files
	}

	image, err := c.provider.Render(ctx, prompt, options)

	if err != nil {
		return nil, err
	}

	id, err := uuid.NewV7()

	if err != nil {
		id = uuid.New()
	}

	path := id.String() + ".png"
	os.MkdirAll(filepath.Join("public", "files"), 0755)

	if err := os.WriteFile(filepath.Join("public", "files", path), image.Content, 0644); err != nil {
		return nil, err
	}

	url, err := url.JoinPath(os.Getenv("BASE_URL"), "files/"+path)

	if err != nil {
		return nil, err
	}

	return Result{
		URL: url,
	}, nil
}
