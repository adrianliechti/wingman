package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"regexp"
	"strconv"
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

	transparent := r.supportsTransparent()

	if len(options.Images) == 0 {
		params := openai.ImageGenerateParams{
			Model:  r.model,
			Prompt: input,
		}

		if size := r.sizeFor(options.Aspect, options.Resolution); size != "" {
			params.Size = openai.ImageGenerateParamsSize(size)
		}

		if quality := r.qualityFor(options.Quality); quality != "" {
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

		if size := r.sizeFor(options.Aspect, options.Resolution); size != "" {
			params.Size = openai.ImageEditParamsSize(size)
		}

		if quality := r.qualityFor(options.Quality); quality != "" {
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

// gpt-image-1 / -mini only support the three base sizes. gpt-image-2 adds the
// 16:9 / 9:16 sizes. gpt-image-2.5 (flare, sunburst) accepts arbitrary
// WIDTHxHEIGHT sizes (multiples of 16, aspect 1:3 to 3:1, up to 4K), so any
// aspect ratio and resolution is mapped to a custom size for those models.
// dall-e is no longer supported.
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

const (
	// custom size limits of gpt-image-2.5
	customSizeStep  = 16
	customSizeEdge  = 3840
	customSizeMinPx = 655360
	customSizeMaxPx = 8294400
	customAspectMin = 1.0 / 3.0
	customAspectMax = 3.0
	customDefaultPx = 1536 * 1024
	customPixels512 = 1024 * 768
	customPixels1K  = 1536 * 1024
	customPixels2K  = 2560 * 1440
	customPixels4K  = 3840 * 2160
)

func (r *Renderer) isGPTImage25() bool {
	return strings.HasPrefix(strings.ToLower(r.model), "gpt-image-2.5")
}

func (r *Renderer) isGPTImage2() bool {
	model := strings.ToLower(r.model)
	return strings.HasPrefix(model, "gpt-image-2") && !strings.HasPrefix(model, "gpt-image-2.5")
}

func (r *Renderer) isGPTImage1() bool {
	return strings.HasPrefix(strings.ToLower(r.model), "gpt-image-1")
}

func (r *Renderer) sizes() []aspectSize {
	if r.isGPTImage2() || r.isGPTImage25() {
		return gptImage2Sizes
	}

	return gptImage1Sizes
}

func (r *Renderer) supportsTransparent() bool {
	return r.isGPTImage1() || r.isGPTImage25()
}

func (r *Renderer) supportsCustomSize() bool {
	return r.isGPTImage25()
}

func (r *Renderer) supportsExtendedQuality() bool {
	return r.isGPTImage25()
}

// sizeFor picks the size to request for the given aspect ratio and
// resolution. Models with a fixed size list get the nearest supported size;
// gpt-image-2.5 gets a custom size once the request leaves the fixed list
// (an aspect ratio not in the list or an explicit resolution).
func (r *Renderer) sizeFor(aspect provider.AspectRatio, resolution provider.Resolution) string {
	sizes := r.sizes()

	if !r.supportsCustomSize() {
		return sizeFor(sizes, aspect)
	}

	if resolution == "" {
		if aspect == "" {
			return ""
		}

		for _, s := range sizes {
			if s.aspect == aspect {
				return s.size
			}
		}
	}

	if aspect == "" {
		aspect = provider.AspectRatio1x1
	}

	return customSize(aspect, resolution)
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

// customSize builds a WIDTHxHEIGHT string for gpt-image-2.5: edges are
// multiples of 16, at most 3840, the aspect ratio is clamped to 1:3..3:1 and
// the pixel count stays within the model's range.
func customSize(aspect provider.AspectRatio, resolution provider.Resolution) string {
	ratio, ok := aspectValue(aspect)

	if !ok {
		return ""
	}

	ratio = math.Min(math.Max(ratio, customAspectMin), customAspectMax)

	pixels := float64(customDefaultPx)

	switch resolution {
	case provider.Resolution512:
		pixels = customPixels512
	case provider.Resolution1K:
		pixels = customPixels1K
	case provider.Resolution2K:
		pixels = customPixels2K
	case provider.Resolution4K:
		pixels = customPixels4K
	}

	width := math.Sqrt(pixels * ratio)
	height := width / ratio

	if width > customSizeEdge {
		width = customSizeEdge
		height = width / ratio
	}

	if height > customSizeEdge {
		height = customSizeEdge
		width = height * ratio
	}

	w := roundStep(width, customSizeStep)
	h := roundStep(height, customSizeStep)

	// rounding can push the aspect ratio just outside the allowed range
	for float64(w)/float64(h) > customAspectMax {
		h += customSizeStep
	}

	for float64(w)/float64(h) < customAspectMin {
		w += customSizeStep
	}

	// rounding can push the pixel count just outside the allowed range
	for w*h > customSizeMaxPx {
		if w >= h {
			w -= customSizeStep
		} else {
			h -= customSizeStep
		}
	}

	for w*h < customSizeMinPx {
		if w <= h {
			w += customSizeStep
		} else {
			h += customSizeStep
		}
	}

	return fmt.Sprintf("%dx%d", w, h)
}

func aspectValue(aspect provider.AspectRatio) (float64, bool) {
	parts := strings.SplitN(strings.TrimSpace(string(aspect)), ":", 2)

	if len(parts) != 2 {
		return 0, false
	}

	w, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	h, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)

	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, false
	}

	return w / h, true
}

func roundStep(value float64, step int) int {
	v := int(math.Round(value/float64(step))) * step

	if v < step {
		return step
	}

	return v
}

// qualityFor maps the requested quality to the model's vocabulary. Only
// gpt-image-2.5 knows xhigh / max; older models get high instead.
func (r *Renderer) qualityFor(quality provider.Quality) string {
	switch quality {
	case provider.QualityXHigh, provider.QualityMax:
		if !r.supportsExtendedQuality() {
			return "high"
		}
	}

	return qualityValue(quality)
}

func qualityValue(quality provider.Quality) string {
	switch quality {
	case provider.QualityLow:
		return "low"
	case provider.QualityMedium:
		return "medium"
	case provider.QualityHigh:
		return "high"
	case provider.QualityXHigh:
		return "xhigh"
	case provider.QualityMax:
		return "max"
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
