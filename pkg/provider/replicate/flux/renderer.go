package flux

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"

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

	FluxKontextDev string = "black-forest-labs/flux-kontext-dev"
	FluxKontextPro string = "black-forest-labs/flux-kontext-pro"
	FluxKontextMax string = "black-forest-labs/flux-kontext-max"
)

var SupportedModels = []string{
	FluxPro,
	FluxDev,
	FluxSchnell,

	FluxPro11,
	FluxProUltra11,

	FluxKontextDev,
	FluxKontextPro,
	FluxKontextMax,
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
		if len(options.Images) > 1 {
			return nil, errors.New("only one image input is supported")
		}

		file, err := r.UploadFile(ctx, options.Images[0])

		if err != nil {
			return nil, err
		}

		fileID := file.ID
		fileURL := file.URLs["get"]

		defer func() {
			r.DeleteFile(context.Background(), fileID)
		}()

		options.Images = []provider.File{
			{
				Name: fileURL,
			},
		}
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
	case FluxSchnell, FluxDev:
		// https://replicate.com/black-forest-labs/flux-schnell/api/schema#input-schema
		// https://replicate.com/black-forest-labs/flux-dev/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"disable_safety_checker": true,
		}

		return input, nil

	case FluxPro, FluxPro11, FluxProUltra11:
		// https://replicate.com/black-forest-labs/flux-pro/api/schema#input-schema
		// https://replicate.com/black-forest-labs/flux-1.1-pro/api/schema#input-schema
		// https://replicate.com/black-forest-labs/flux-1.1-pro-ultra/api/schema#input-schema

		input := map[string]any{
			"prompt": prompt,

			"aspect_ratio":  "3:2",
			"output_format": "png",

			"safety_tolerance": 6,
		}

		return input, nil

	case FluxKontextDev:
		if options == nil || len(options.Images) == 0 {
			return nil, errors.New("image input required")
		}

		image := options.Images[0]

		var imageURL string

		if strings.HasPrefix(image.Name, "https://") || strings.HasPrefix(image.Name, "http://") {
			imageURL = image.Name
		}

		if imageURL == "" {
			return nil, errors.New("image URL required")
		}

		// https://replicate.com/black-forest-labs/flux-kontext-dev/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"input_image":   imageURL,
			"output_format": "png",

			"disable_safety_checker": true,
		}

		return input, nil
	case FluxKontextPro, FluxKontextMax:
		if options == nil || len(options.Images) == 0 {
			return nil, errors.New("image input required")
		}

		image := options.Images[0]

		var imageURL string

		if strings.HasPrefix(image.Name, "https://") || strings.HasPrefix(image.Name, "http://") {
			imageURL = image.Name
		}

		if imageURL == "" {
			return nil, errors.New("image URL required")
		}

		// https://replicate.com/black-forest-labs/flux-kontext-pro/api/schema#input-schema
		// https://replicate.com/black-forest-labs/flux-kontext-max/api/schema#input-schema
		input := map[string]any{
			"prompt": prompt,

			"input_image":   imageURL,
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
