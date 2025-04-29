package speak

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
	provider provider.Synthesizer
}

func New(provider provider.Synthesizer, options ...Option) (*Client, error) {
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
			Name:        "speak_text",
			Description: "Synthesize speech from text using a TTS (text-to-speech) model on a input prompt. Returns a URL to the generated audio file. Render the URL as markdown ```[prompt](url)```",

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "text to generate audio for in orgiginal language",
					},
				},

				"required": []string{"prompt"},
			},
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	if name != "speak_text" {
		return nil, tool.ErrInvalidTool
	}

	prompt, ok := parameters["prompt"].(string)

	if !ok {
		return nil, errors.New("missing prompt parameter")
	}

	options := &provider.SynthesizeOptions{}

	synthesis, err := c.provider.Synthesize(ctx, prompt, options)

	if err != nil {
		return nil, err
	}

	id, err := uuid.NewV7()

	if err != nil {
		id = uuid.New()
	}

	path := id.String() + ".mp3"
	os.MkdirAll(filepath.Join("public", "files"), 0755)

	if err := os.WriteFile(filepath.Join("public", "files", path), synthesis.Content, 0644); err != nil {
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
