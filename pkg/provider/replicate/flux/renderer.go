package flux

import (
	"context"
	"errors"
	"io"
	"slices"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/replicate"
	"github.com/google/uuid"
)

type Renderer struct {
	*replicate.Client

	model string
}

const (
	FluxSchnell string = "black-forest-labs/flux-schnell"
	FluxDev     string = "black-forest-labs/flux-dev"
	FluxPro     string = "black-forest-labs/flux-pro"

	FluxPro11      string = "black-forest-labs/flux-1.1-pro"
	FluxProUltra11 string = "black-forest-labs/flux-1.1-pro-ultra"
)

var SupportedModels = []string{
	FluxPro,
	FluxDev,
	FluxSchnell,

	FluxPro11,
	FluxProUltra11,
}

func NewRenderer(model string, options ...replicate.Option) (*Renderer, error) {
	if !slices.Contains(SupportedModels, model) {
		return nil, errors.New("unsupported model")
	}

	client, err := replicate.New(model, options...)

	if err != nil {
		return nil, err
	}

	return &Renderer{
		Client: client,

		model: model,
	}, nil
}

func (r *Renderer) Render(ctx context.Context, prompt string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	if len(options.Images) > 0 {
		return nil, errors.New("image input not supported")
	}

	input, err := r.convertInput(prompt, options)

	if err != nil {
		return nil, err
	}

	resp, err := r.Run(ctx, input)

	if err != nil {
		return nil, err
	}

	return r.convertImage(resp)
}

func (r *Renderer) convertInput(prompt string, options *provider.RenderOptions) (replicate.PredictionInput, error) {
	switch r.model {
	case FluxSchnell:
		// https://replicate.com/black-forest-labs/flux-schnell/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"disable_safety_checker": true,
		}

		return input, nil

	case FluxDev:
		// https://replicate.com/black-forest-labs/flux-dev/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"disable_safety_checker": true,
		}

		return input, nil

	case FluxPro:
		// https://replicate.com/black-forest-labs/flux-pro/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"safety_tolerance": 6,
		}

		return input, nil

	case FluxPro11:
		// https://replicate.com/black-forest-labs/flux-1.1-pro/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"safety_tolerance": 6,
		}

		return input, nil

	case FluxProUltra11:
		// https://replicate.com/black-forest-labs/flux-1.1-pro-ultra/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"safety_tolerance": 6,
		}

		return input, nil
	}

	return nil, errors.New("unsupported model")
}

func (r *Renderer) convertImage(output replicate.PredictionOutput) (*provider.Rendering, error) {
	file, ok := output.(*replicate.FileOutput)

	if !ok {
		return nil, errors.New("unsupported output")
	}

	//url, _ := url.Parse(file.URL)

	data, err := io.ReadAll(file)

	if err != nil {
		return nil, err
	}

	return &provider.Rendering{
		ID:    uuid.New().String(),
		Model: r.model,

		Content:     data,
		ContentType: "image/png",
	}, nil
}
