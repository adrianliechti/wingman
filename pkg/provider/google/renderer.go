package google

import (
	"context"

	"github.com/adrianliechti/wingman/pkg/provider"
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

	image, err := client.Models.GenerateContent(ctx, r.model, contents, imageConfig(options))

	if err != nil {
		return nil, convertError(err)
	}

	result := &provider.Rendering{
		ID:    image.ResponseID,
		Model: r.model,
	}

	for _, part := range image.Candidates[0].Content.Parts {
		if part.InlineData == nil {
			continue
		}

		result.Content = part.InlineData.Data
		result.ContentType = part.InlineData.MIMEType

		break
	}

	return result, nil
}

var googleAspects = []provider.AspectRatio{
	provider.AspectRatio1x1,
	provider.AspectRatio2x3,
	provider.AspectRatio3x2,
	provider.AspectRatio3x4,
	provider.AspectRatio4x3,
	provider.AspectRatio4x5,
	provider.AspectRatio5x4,
	provider.AspectRatio9x16,
	provider.AspectRatio16x9,
	provider.AspectRatio21x9,
}

func imageConfig(options *provider.RenderOptions) *genai.GenerateContentConfig {
	config := &genai.ImageConfig{}

	if options.Aspect != "" {
		config.AspectRatio = string(options.Aspect.Nearest(googleAspects))
	}

	switch options.Resolution {
	case provider.Resolution512:
		config.ImageSize = "0.5K"
	case provider.Resolution1K:
		config.ImageSize = "1K"
	case provider.Resolution2K:
		config.ImageSize = "2K"
	case provider.Resolution4K:
		config.ImageSize = "4K"
	}

	if config.AspectRatio == "" && config.ImageSize == "" {
		return nil
	}

	return &genai.GenerateContentConfig{
		ImageConfig: config,
	}
}
