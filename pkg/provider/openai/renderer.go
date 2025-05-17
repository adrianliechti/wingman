package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/google/uuid"

	"github.com/openai/openai-go"
)

var _ provider.Renderer = (*Renderer)(nil)

type Renderer struct {
	*Config
	images openai.ImageService
}

func NewRenderer(url, model string, options ...Option) (*Renderer, error) {
	cfg := &Config{
		url:   url,
		model: model,
	}

	for _, option := range options {
		option(cfg)
	}

	return &Renderer{
		Config: cfg,
		images: openai.NewImageService(cfg.Options()...),
	}, nil
}

func (r *Renderer) Render(ctx context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	if options == nil {
		options = new(provider.RenderOptions)
	}

	result := &provider.Rendering{
		ID:    uuid.NewString(),
		Model: r.model,
	}

	if len(options.Images) == 0 {
		image, err := r.images.Generate(ctx, openai.ImageGenerateParams{
			Model:  r.model,
			Prompt: input,
		})

		if err != nil {
			return nil, convertError(err)
		}

		data, err := r.getData(ctx, image.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	} else {
		var files []io.Reader

		for n, i := range options.Images {
			var imageName string
			var imageType string

			if i.ContentType != "" {
				switch i.ContentType {
				case "image/jpeg":
					imageName = fmt.Sprintf("image-%d.jpg", n)
					imageType = "image/jpeg"

				case "image/png":
					imageName = fmt.Sprintf("image-%d.png", n)
					imageType = "image/png"

				case "image/webp":
					imageName = fmt.Sprintf("image-%d.webp", n)
					imageType = "image/webp"
				}
			} else if i.Name != "" {
				switch path.Ext(imageName) {
				case ".jpg", ".jpeg", ".jpe":
					imageName = fmt.Sprintf("image-%d.jpg", n)
					imageType = "image/jpeg"

				case ".png":
					imageName = fmt.Sprintf("image-%d.png", n)
					imageType = "image/png"

				case ".webp":
					imageName = fmt.Sprintf("image-%d.webp", n)
					imageType = "image/webp"
				}
			}

			if imageName == "" || imageType == "" {
				return nil, errors.New("invalid image name or type")
			}

			files = append(files, openai.File(bytes.NewReader(i.Content), imageName, imageType))
		}

		image, err := r.images.Edit(ctx, openai.ImageEditParams{
			Model:  r.model,
			Prompt: input,

			Image: openai.ImageEditParamsImageUnion{
				OfBinaryArray: files,
			},
		})

		if err != nil {
			return nil, convertError(err)
		}

		data, err := r.getData(ctx, image.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = "image/png"
	}

	return result, nil
}

func (r *Renderer) getData(ctx context.Context, image openai.Image) ([]byte, error) {
	if image.URL != "" {
		if strings.HasPrefix(image.URL, "data:") {
			re := regexp.MustCompile(`data:([a-zA-Z]+\/[a-zA-Z0-9.+_-]+);base64,\s*(.+)`)

			match := re.FindStringSubmatch(image.URL)

			if len(match) != 3 {
				return nil, fmt.Errorf("invalid data url")
			}

			return base64.StdEncoding.DecodeString(match[2])
		}

		req, err := http.NewRequestWithContext(ctx, "GET", image.URL, nil)

		if err != nil {
			return nil, err
		}

		resp, err := r.client.Do(req)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		return io.ReadAll(resp.Body)
	}

	if image.B64JSON != "" {
		return base64.StdEncoding.DecodeString(image.B64JSON)
	}

	return nil, errors.New("invalid image data")
}
