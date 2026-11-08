package provider

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

type Renderer interface {
	Render(ctx context.Context, input string, options *RenderOptions) (*Rendering, error)
}

type RenderOptions struct {
	// Images are the source and reference images of an edit. Mask and Markup
	// always refer to the first one.
	Images []File

	// Mask marks where the first image may change: fully transparent pixels
	// are editable, everything else is kept.
	Mask *File

	// Markup is a transparent PNG with annotations (circles, arrows, notes)
	// drawn over the first image. The annotations are editing instructions,
	// not content.
	Markup *File

	Aspect     AspectRatio
	Quality    Quality
	Resolution Resolution
	Complexity string

	// Duration is the requested video length; nil uses the model default.
	Duration   *time.Duration
	FrameCount *int64
	// FrameDuration is the animated GIF time per frame; nil uses the model default.
	FrameDuration *time.Duration
	SVGModel      string

	Background Background
	Format     RenderFormat
}

type Rendering struct {
	ID    string
	Model string

	Content     []byte
	ContentType string
}

type AspectRatio string

const (
	AspectRatio1x1  AspectRatio = "1:1"
	AspectRatio2x3  AspectRatio = "2:3"
	AspectRatio3x2  AspectRatio = "3:2"
	AspectRatio3x4  AspectRatio = "3:4"
	AspectRatio4x3  AspectRatio = "4:3"
	AspectRatio4x5  AspectRatio = "4:5"
	AspectRatio5x4  AspectRatio = "5:4"
	AspectRatio9x16 AspectRatio = "9:16"
	AspectRatio16x9 AspectRatio = "16:9"
	AspectRatio21x9 AspectRatio = "21:9"
	AspectRatio1x4  AspectRatio = "1:4"
	AspectRatio4x1  AspectRatio = "4:1"
	AspectRatio1x8  AspectRatio = "1:8"
	AspectRatio8x1  AspectRatio = "8:1"
)

type Quality string

const (
	QualityLow    Quality = "low"
	QualityMedium Quality = "medium"
	QualityHigh   Quality = "high"
	QualityXHigh  Quality = "xhigh"
	QualityMax    Quality = "max"
)

type Resolution string

const (
	Resolution360p  Resolution = "360p"
	Resolution720p  Resolution = "720p"
	Resolution1080p Resolution = "1080p"
	Resolution512   Resolution = "0.5K"
	Resolution1K    Resolution = "1K"
	Resolution2K    Resolution = "2K"
	Resolution4K    Resolution = "4K"
)

type Background string

const (
	BackgroundTransparent Background = "transparent"
	BackgroundOpaque      Background = "opaque"
)

type RenderFormat string

// ImageFormat is retained for existing image renderer callers.
type ImageFormat = RenderFormat

const RenderFormatMP4 RenderFormat = "mp4"

// ValidateImageRenderOptions prevents video requests from silently producing images.
func ValidateImageRenderOptions(options *RenderOptions) error {
	if options.Format == RenderFormatMP4 || options.Duration != nil {
		return errors.New("video output requires a video renderer model")
	}
	return nil
}

const (
	ImageFormatPNG  ImageFormat = "png"
	ImageFormatJPEG ImageFormat = "jpeg"
	ImageFormatWEBP ImageFormat = "webp"
	ImageFormatGIF  ImageFormat = "gif"
	ImageFormatSVG  ImageFormat = "svg"
)

func ParseAspect(value string) AspectRatio {
	aspect := AspectRatio(strings.TrimSpace(value))

	if _, ok := aspect.ratio(); !ok {
		return ""
	}

	return aspect
}

func ParseQuality(value string) Quality {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		return QualityLow
	case "medium":
		return QualityMedium
	case "high":
		return QualityHigh
	case "xhigh", "x-high", "extra-high":
		return QualityXHigh
	case "max", "maximum":
		return QualityMax
	}

	return ""
}

func ParseResolution(value string) Resolution {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "360P":
		return Resolution360p
	case "720P":
		return Resolution720p
	case "1080P":
		return Resolution1080p
	case "0.5K", "512", "512PX":
		return Resolution512
	case "1K", "1024":
		return Resolution1K
	case "2K":
		return Resolution2K
	case "4K":
		return Resolution4K
	}

	return ""
}

func ParseBackground(value string) Background {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "transparent":
		return BackgroundTransparent
	case "opaque":
		return BackgroundOpaque
	}

	return ""
}

func ParseFormat(value string) RenderFormat {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mp4":
		return RenderFormatMP4
	case "png":
		return ImageFormatPNG
	case "jpeg", "jpg":
		return ImageFormatJPEG
	case "webp":
		return ImageFormatWEBP
	case "gif":
		return ImageFormatGIF
	case "svg":
		return ImageFormatSVG
	}

	return ""
}

func ParseSizeAspect(size string) AspectRatio {
	size = strings.ToLower(strings.TrimSpace(size))

	if size == "" || size == "auto" {
		return ""
	}

	parts := strings.SplitN(size, "x", 2)

	if len(parts) != 2 {
		return ""
	}

	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))

	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return ""
	}

	g := gcd(w, h)

	return AspectRatio(strconv.Itoa(w/g) + ":" + strconv.Itoa(h/g))
}

func (a AspectRatio) Nearest(supported []AspectRatio) AspectRatio {
	target, ok := a.ratio()

	if !ok || len(supported) == 0 {
		return a
	}

	best := supported[0]
	distance := math.MaxFloat64

	for _, s := range supported {
		r, ok := s.ratio()

		if !ok {
			continue
		}

		d := math.Abs(math.Log(target) - math.Log(r))

		if d < distance {
			distance = d
			best = s
		}
	}

	return best
}

func (a AspectRatio) ratio() (float64, bool) {
	parts := strings.SplitN(strings.TrimSpace(string(a)), ":", 2)

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

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}

	return a
}
