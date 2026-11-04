package provider

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"slices"

	_ "golang.org/x/image/webp"
)

// Mask and Markup are "edit guides": extra images that steer an edit of the
// first input image. Providers without native support use input images and
// prompt instructions to approximate the requested edit.

// MaskInstruction tells a model what the appended mask image means.
const MaskInstruction = "The last image is a mask for the first image: white areas mark the only region that may change, black areas must stay exactly as they are. Apply the edit below only inside the white region and do not include the mask in the result."

// MarkupInstruction tells a model that the first image carries annotations.
const MarkupInstruction = "The first image has annotations (lines, circles, arrows, notes) drawn on top of it. Treat them as editing instructions: apply what they ask for and remove the annotations themselves from the result."

// MarkupReferenceInstruction identifies an annotated reference while leaving
// the first image intact, including regions protected by a mask.
func MarkupReferenceInstruction(index int) string {
	return fmt.Sprintf("Image %d is an annotated copy of the first image. Treat its annotations (lines, circles, arrows, notes) as editing instructions for the first image. Do not include the annotations in the result.", index)
}

// ErrEditGuideWithoutImage is returned when a mask or markup arrives without
// an image to apply it to.
var ErrEditGuideWithoutImage = &ProviderError{
	Code:    http.StatusBadRequest,
	Type:    "invalid_request_error",
	Message: "mask and markup require an input image",
}

// ValidateEditGuides checks that PNG guides match the first input image.
func ValidateEditGuides(images []File, mask, markup *File) error {
	if mask == nil && markup == nil {
		return nil
	}

	if len(images) == 0 {
		return ErrEditGuideWithoutImage
	}

	base, _, err := image.DecodeConfig(bytes.NewReader(images[0].Content))
	if err != nil {
		return InvalidRequest(fmt.Errorf("image: %w", err))
	}

	for _, guide := range []struct {
		name string
		file *File
	}{{"mask", mask}, {"markup", markup}} {
		if guide.file == nil {
			continue
		}

		config, format, err := image.DecodeConfig(bytes.NewReader(guide.file.Content))
		if err != nil {
			return InvalidRequest(fmt.Errorf("%s: %w", guide.name, err))
		}
		if format != "png" {
			return InvalidRequest(fmt.Errorf("%s must be a PNG image", guide.name))
		}
		if config.Width != base.Width || config.Height != base.Height {
			return InvalidRequest(fmt.Errorf("%s size %dx%d does not match image size %dx%d", guide.name, config.Width, config.Height, base.Width, base.Height))
		}
	}

	return nil
}

// EmulateEditGuides rewrites a mask or markup edit for models without native
// support. Markup is composited onto the first image, or appended as an
// annotated reference when a mask is also present. The mask is appended as
// a black-and-white image. Images and prompt are unchanged without guides.
func EmulateEditGuides(prompt string, images []File, mask, markup *File) (string, []File, error) {
	if mask == nil && markup == nil {
		return prompt, images, nil
	}

	if err := ValidateEditGuides(images, mask, markup); err != nil {
		return "", nil, err
	}

	images = slices.Clone(images)

	var instructions string

	if markup != nil {
		marked, err := ApplyMarkup(images[0], *markup)

		if err != nil {
			return "", nil, err
		}

		if mask != nil {
			images = append(images, marked)
			instructions += MarkupReferenceInstruction(len(images)) + "\n\n"
		} else {
			images[0] = marked
			instructions += MarkupInstruction + "\n\n"
		}
	}

	if mask != nil {
		rendered, err := RenderMask(*mask)

		if err != nil {
			return "", nil, err
		}

		images = append(images, rendered)
		instructions += MaskInstruction + "\n\n"
	}

	return instructions + prompt, images, nil
}

// ApplyMarkup composites a transparent markup overlay onto an image and
// returns the result as PNG. Both must have the same dimensions.
func ApplyMarkup(base, markup File) (File, error) {
	img, err := decodeImage(base)

	if err != nil {
		return File{}, InvalidRequest(fmt.Errorf("image: %w", err))
	}

	overlay, err := decodeImage(markup)

	if err != nil {
		return File{}, InvalidRequest(fmt.Errorf("markup: %w", err))
	}

	if img.Bounds().Size() != overlay.Bounds().Size() {
		return File{}, &ProviderError{
			Code:    http.StatusBadRequest,
			Type:    "invalid_request_error",
			Message: fmt.Sprintf("markup size %s does not match image size %s", overlay.Bounds().Size(), img.Bounds().Size()),
		}
	}

	dst := image.NewNRGBA(image.Rectangle{Max: img.Bounds().Size()})

	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	draw.Draw(dst, dst.Bounds(), overlay, overlay.Bounds().Min, draw.Over)

	return encodePNG("image.png", dst)
}

// RenderMask converts an alpha mask (transparent = editable) into a
// black-and-white PNG where white marks the editable region.
func RenderMask(mask File) (File, error) {
	img, err := decodeImage(mask)

	if err != nil {
		return File{}, InvalidRequest(fmt.Errorf("mask: %w", err))
	}

	bounds := img.Bounds()
	dst := image.NewGray(image.Rectangle{Max: bounds.Size()})

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()

			value := color.Gray{Y: 0}

			if a == 0 {
				value.Y = 0xff
			}

			dst.SetGray(x-bounds.Min.X, y-bounds.Min.Y, value)
		}
	}

	return encodePNG("mask.png", dst)
}

func decodeImage(file File) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(file.Content))
	return img, err
}

func encodePNG(name string, img image.Image) (File, error) {
	var buf bytes.Buffer

	if err := png.Encode(&buf, img); err != nil {
		return File{}, err
	}

	return File{
		Name:        name,
		Content:     buf.Bytes(),
		ContentType: "image/png",
	}, nil
}
