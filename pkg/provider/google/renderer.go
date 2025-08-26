package google

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
	"google.golang.org/genai"
)

var _ provider.Renderer = (*Renderer)(nil)

type Renderer struct {
	*Config
}

func NewRenderer(model string, options ...Option) (*Renderer, error) {
	cfg := &Config{
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Renderer{
		Config: cfg,
	}, nil
}

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	client, err := r.newClient(ctx)

	if err != nil {
		return nil, err
	}

	parts := []*genai.Part{
		genai.NewPartFromText(input),
	}

	for _, i := range options.Images {
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: i.ContentType,
				Data:     i.Content,
			},
		})
	}

	contents := []*genai.Content{
		genai.NewContentFromParts(parts, genai.RoleUser),
	}

	image, err := client.Models.GenerateContent(ctx, r.model, contents, nil)

	if err != nil {
		return nil, err
	}

	result := &provider.Rendering{
		ID:    uuid.NewString(),
		Model: r.model,
	}

	for _, part := range image.Candidates[0].Content.Parts {
		if part.InlineData == nil {
			continue
		}

		result.Content = part.InlineData.Data
		result.ContentType = part.InlineData.MIMEType
	}

	return result, nil
}
