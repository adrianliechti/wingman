package openrouter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"
)

var _ provider.Renderer = (*Renderer)(nil)

type Renderer struct {
	*Config
}

func NewRenderer(model string, options ...Option) (*Renderer, error) {
	return &Renderer{
		Config: newConfig(model, options...),
	}, nil
}

type imagesResponse struct {
	Data []imageData `json:"data"`
}

type imageData struct {
	B64JSON   string `json:"b64_json"`
	MediaType string `json:"media_type"`
}

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	body := map[string]any{
		"model":  r.model,
		"prompt": input,
	}

	if options.Aspect != "" {
		body["aspect_ratio"] = string(options.Aspect)
	}

	if options.Quality != "" {
		body["quality"] = string(options.Quality)
	}

	if resolution := resolution(options.Resolution); resolution != "" {
		body["resolution"] = resolution
	}

	if options.Background != "" {
		body["background"] = string(options.Background)
	}

	if options.Format != "" {
		body["output_format"] = string(options.Format)
	}

	if len(options.Images) > 0 {
		references := make([]map[string]any, 0, len(options.Images))

		for _, img := range options.Images {
			contentType := img.ContentType

			if contentType == "" {
				contentType = "image/png"
			}

			dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(img.Content))

			references = append(references, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": dataURL,
				},
			})
		}

		body["input_references"] = references
	}

	var result imagesResponse

	if err := doRequest(ctx, r.client, r.url+"/images", r.token, body, &result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 || result.Data[0].B64JSON == "" {
		return nil, errors.New("no image data in response")
	}

	data, err := base64.StdEncoding.DecodeString(result.Data[0].B64JSON)

	if err != nil {
		return nil, err
	}

	contentType := result.Data[0].MediaType

	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	return &provider.Rendering{
		ID:    uuid.NewString(),
		Model: r.model,

		Content:     data,
		ContentType: contentType,
	}, nil
}

func resolution(value provider.Resolution) string {
	switch value {
	case provider.Resolution512:
		return "512"
	case provider.Resolution1K:
		return "1K"
	case provider.Resolution2K:
		return "2K"
	case provider.Resolution4K:
		return "4K"
	}

	return ""
}
