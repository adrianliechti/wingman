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

	"github.com/openai/openai-go/v3"
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
		images: openai.NewImageService(cfg.AzureOptions()...),
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

	sizes := r.sizes()
	transparent := r.supportsTransparent()

	if len(options.Images) == 0 {
		params := openai.ImageGenerateParams{
			Model:  r.model,
			Prompt: input,
		}

		if size := sizeFor(sizes, options.Aspect); size != "" {
			params.Size = openai.ImageGenerateParamsSize(size)
		}

		if quality := qualityValue(options.Quality); quality != "" {
			params.Quality = openai.ImageGenerateParamsQuality(quality)
		}

		background := backgroundFor(options.Background, transparent)

		if background != "" {
			params.Background = openai.ImageGenerateParamsBackground(background)
		}

		if format := outputFormat(options.Format, background); format != "" {
			params.OutputFormat = openai.ImageGenerateParamsOutputFormat(format)
		}

		image, err := r.images.Generate(ctx, params)

		if err != nil {
			return nil, convertError(err)
		}

		data, err := r.getData(ctx, image.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = http.DetectContentType(data)
	} else {
		var files []io.Reader

		for n, i := range options.Images {
			var imageName string
			var imageType string

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

			if imageType == "" {
				switch path.Ext(i.Name) {
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

			if imageType == "" {
				switch http.DetectContentType(i.Content) {
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
			}

			if imageName == "" || imageType == "" {
				return nil, errors.New("invalid image name or type")
			}

			files = append(files, openai.File(bytes.NewReader(i.Content), imageName, imageType))
		}

		params := openai.ImageEditParams{
			Model:  r.model,
			Prompt: input,

			Image: openai.ImageEditParamsImageUnion{
				OfFileArray: files,
			},
		}

		if size := sizeFor(sizes, options.Aspect); size != "" {
			params.Size = openai.ImageEditParamsSize(size)
		}

		if quality := qualityValue(options.Quality); quality != "" {
			params.Quality = openai.ImageEditParamsQuality(quality)
		}

		background := backgroundFor(options.Background, transparent)

		if background != "" {
			params.Background = openai.ImageEditParamsBackground(background)
		}

		if format := outputFormat(options.Format, background); format != "" {
			params.OutputFormat = openai.ImageEditParamsOutputFormat(format)
		}

		image, err := r.images.Edit(ctx, params)

		if err != nil {
			return nil, convertError(err)
		}

		data, err := r.getData(ctx, image.Data[0])

		if err != nil {
			return nil, err
		}

		result.Content = data
		result.ContentType = http.DetectContentType(data)
	}

	return result, nil
}

type aspectSize struct {
	aspect provider.AspectRatio
	size   string
}

// Only gpt-image-2 supports the 16:9 / 9:16 sizes; only gpt-image-1 / -mini
// support a transparent background. dall-e is no longer supported.
var (
	gptImage1Sizes = []aspectSize{
		{provider.AspectRatio1x1, "1024x1024"},
		{provider.AspectRatio3x2, "1536x1024"},
		{provider.AspectRatio2x3, "1024x1536"},
	}

	gptImage2Sizes = []aspectSize{
		{provider.AspectRatio1x1, "1024x1024"},
		{provider.AspectRatio3x2, "1536x1024"},
		{provider.AspectRatio2x3, "1024x1536"},
		{provider.AspectRatio16x9, "1536x864"},
		{provider.AspectRatio9x16, "864x1536"},
	}
)

func (r *Renderer) sizes() []aspectSize {
	if strings.HasPrefix(strings.ToLower(r.model), "gpt-image-2") {
		return gptImage2Sizes
	}

	return gptImage1Sizes
}

func (r *Renderer) supportsTransparent() bool {
	return strings.HasPrefix(strings.ToLower(r.model), "gpt-image-1")
}

// sizeFor maps a requested aspect ratio to the nearest pixel size the model
// supports, or "" when none was requested.
func sizeFor(sizes []aspectSize, aspect provider.AspectRatio) string {
	if aspect == "" || len(sizes) == 0 {
		return ""
	}

	supported := make([]provider.AspectRatio, len(sizes))

	for i, s := range sizes {
		supported[i] = s.aspect
	}

	nearest := aspect.Nearest(supported)

	for _, s := range sizes {
		if s.aspect == nearest {
			return s.size
		}
	}

	return ""
}

func qualityValue(quality provider.Quality) string {
	switch quality {
	case provider.QualityLow:
		return "low"
	case provider.QualityMedium:
		return "medium"
	case provider.QualityHigh:
		return "high"
	}

	return ""
}

func backgroundValue(background provider.Background) string {
	switch background {
	case provider.BackgroundTransparent:
		return "transparent"
	case provider.BackgroundOpaque:
		return "opaque"
	}

	return ""
}

func backgroundFor(background provider.Background, transparent bool) string {
	if background == provider.BackgroundTransparent && !transparent {
		return ""
	}

	return backgroundValue(background)
}

func formatValue(format provider.ImageFormat) string {
	switch format {
	case provider.ImageFormatPNG:
		return "png"
	case provider.ImageFormatJPEG:
		return "jpeg"
	case provider.ImageFormatWEBP:
		return "webp"
	}

	return ""
}

// The edits endpoint flattens a transparent background unless an alpha-capable
// format is set, so default to png when transparent and no format was requested.
func outputFormat(format provider.ImageFormat, background string) string {
	if v := formatValue(format); v != "" {
		return v
	}

	if background == "transparent" {
		return "png"
	}

	return ""
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
