package google

import (
	"context"
	"errors"
	"slices"
	"strings"

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

	if strings.HasPrefix(strings.ToLower(r.model), "gemini-omni-") {
		return r.renderVideo(ctx, input, options)
	}
	if err := provider.ValidateImageRenderOptions(options); err != nil {
		return nil, err
	}

	// Gemini has no mask or markup field (inpainting is prompt-driven), so
	// both guides become input images with an instruction.
	prompt, images, err := provider.EmulateEditGuides(input, options.Images, options.Mask, options.Markup)

	if err != nil {
		return nil, err
	}

	client, err := r.newClient(ctx)
	if err != nil {
		return nil, err
	}

	parts := []*genai.Part{
		genai.NewPartFromText(prompt),
	}

	for _, i := range images {
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

	image, err := client.Models.GenerateContent(ctx, r.model, contents, imageConfig(options, r.aspects()))

	if err != nil {
		return nil, convertError(err)
	}

	for _, candidate := range image.Candidates {
		if candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil {
				continue
			}

			return &provider.Rendering{
				ID:    image.ResponseID,
				Model: r.model,

				Content:     part.InlineData.Data,
				ContentType: part.InlineData.MIMEType,
			}, nil
		}
	}

	// A blocked prompt or a refused candidate carries no image.
	reason := "no image in response"

	if feedback := image.PromptFeedback; feedback != nil && feedback.BlockReason != "" {
		reason = "prompt blocked: " + string(feedback.BlockReason)
	} else if len(image.Candidates) > 0 && image.Candidates[0].FinishReason != "" {
		reason = "no image in response: " + string(image.Candidates[0].FinishReason)
	}

	return nil, errors.New("gemini: " + reason)
}

// googleAspects are the ratios every Gemini image model accepts.
// googleAspectsExtended adds the extreme ratios of Nano Banana 2.1 and
// Gemini 3.1 Flash Image, which Gemini 3 Pro Image and 2.5 Flash Image reject.
var (
	googleAspects = []provider.AspectRatio{
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

	googleAspectsExtended = append(slices.Clone(googleAspects),
		provider.AspectRatio1x4,
		provider.AspectRatio4x1,
		provider.AspectRatio1x8,
		provider.AspectRatio8x1,
	)
)

func (r *Renderer) aspects() []provider.AspectRatio {
	model := strings.ToLower(r.model)

	if strings.Contains(model, "nano-banana-2.1") || strings.Contains(model, "3.1-flash-image") || strings.Contains(model, "3.1-flash-lite-image") {
		return googleAspectsExtended
	}

	return googleAspects
}

func imageConfig(options *provider.RenderOptions, aspects []provider.AspectRatio) *genai.GenerateContentConfig {
	config := &genai.ImageConfig{}

	if options.Aspect != "" {
		config.AspectRatio = string(options.Aspect.Nearest(aspects))
	}

	switch options.Resolution {
	case provider.Resolution512:
		config.ImageSize = "512"
	case provider.Resolution1K:
		config.ImageSize = "1K"
	case provider.Resolution2K:
		config.ImageSize = "2K"
	case provider.Resolution4K:
		config.ImageSize = "4K"
	}

	if config.AspectRatio == "" && config.ImageSize == "" {
		config = nil
	}

	return &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE"},
		ImageConfig:        config,
	}
}
