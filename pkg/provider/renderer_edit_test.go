package provider

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngFile(t *testing.T, name string, img image.Image) File {
	t.Helper()

	var buf bytes.Buffer

	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	return File{Name: name, Content: buf.Bytes(), ContentType: "image/png"}
}

func decodePNG(t *testing.T, file File) image.Image {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(file.Content))

	if err != nil {
		t.Fatal(err)
	}

	return img
}

// solid returns a 4x4 opaque image of one color.
func solid(c color.Color) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))

	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, c)
		}
	}

	return img
}

// overlay returns a 4x4 transparent image with one opaque red pixel at (1,1).
func overlay() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})

	return img
}

func TestApplyMarkup(t *testing.T) {
	base := pngFile(t, "base.png", solid(color.NRGBA{B: 255, A: 255}))
	markup := pngFile(t, "markup.png", overlay())

	got, err := ApplyMarkup(base, markup)

	if err != nil {
		t.Fatal(err)
	}

	if got.ContentType != "image/png" {
		t.Errorf("content type %q, want image/png", got.ContentType)
	}

	img := decodePNG(t, got)

	if r, _, b, _ := img.At(1, 1).RGBA(); r == 0 || b != 0 {
		t.Errorf("pixel under the annotation stayed %v, want red", img.At(1, 1))
	}

	if r, _, b, _ := img.At(0, 0).RGBA(); r != 0 || b == 0 {
		t.Errorf("pixel outside the annotation changed to %v, want blue", img.At(0, 0))
	}
}

func TestApplyMarkupSizeMismatch(t *testing.T) {
	base := pngFile(t, "base.png", solid(color.White))
	markup := pngFile(t, "markup.png", image.NewNRGBA(image.Rect(0, 0, 2, 2)))

	_, err := ApplyMarkup(base, markup)

	if err == nil {
		t.Fatal("expected size mismatch error")
	}

	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestRenderMask(t *testing.T) {
	mask := image.NewNRGBA(image.Rect(0, 0, 4, 4))

	for y := range 4 {
		for x := range 4 {
			mask.Set(x, y, color.NRGBA{R: 20, G: 30, B: 40, A: 255})
		}
	}

	// transparent hole: editable
	mask.Set(2, 2, color.NRGBA{})
	// Partially transparent pixels remain protected, even at low alpha.
	mask.Set(3, 3, color.NRGBA{R: 255, A: 10})

	got, err := RenderMask(pngFile(t, "mask.png", mask))

	if err != nil {
		t.Fatal(err)
	}

	img := decodePNG(t, got)

	white := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r == 0xffff && g == 0xffff && b == 0xffff
	}

	if !white(2, 2) || white(3, 3) {
		t.Errorf("only fully transparent pixels must be white, got %v %v", img.At(2, 2), img.At(3, 3))
	}

	if white(0, 0) {
		t.Errorf("opaque pixel must be black, got %v", img.At(0, 0))
	}
}

func TestEmulateEditGuides(t *testing.T) {
	base := pngFile(t, "base.png", solid(color.White))
	ref := pngFile(t, "ref.png", solid(color.Black))
	mask := pngFile(t, "mask.png", overlay())
	markup := pngFile(t, "markup.png", overlay())

	t.Run("none", func(t *testing.T) {
		prompt, images, err := EmulateEditGuides("hello", []File{base}, nil, nil)

		if err != nil || prompt != "hello" || len(images) != 1 {
			t.Fatalf("got %q %d %v", prompt, len(images), err)
		}
	})

	t.Run("without image", func(t *testing.T) {
		_, _, err := EmulateEditGuides("hello", nil, &mask, nil)

		if err != ErrEditGuideWithoutImage {
			t.Fatalf("got %v, want ErrEditGuideWithoutImage", err)
		}
	})

	t.Run("mask", func(t *testing.T) {
		prompt, images, err := EmulateEditGuides("hello", []File{base, ref}, &mask, nil)

		if err != nil {
			t.Fatal(err)
		}

		if len(images) != 3 || images[2].Name != "mask.png" {
			t.Errorf("mask must be appended last, got %d images", len(images))
		}

		if !strings.HasPrefix(prompt, MaskInstruction) || !strings.HasSuffix(prompt, "hello") {
			t.Errorf("prompt %q", prompt)
		}
	})

	t.Run("markup", func(t *testing.T) {
		prompt, images, err := EmulateEditGuides("hello", []File{base, ref}, nil, &markup)

		if err != nil {
			t.Fatal(err)
		}

		if len(images) != 2 {
			t.Errorf("markup must not add images, got %d", len(images))
		}

		if bytes.Equal(images[0].Content, base.Content) {
			t.Error("first image must carry the composited markup")
		}

		if !strings.HasPrefix(prompt, MarkupInstruction) {
			t.Errorf("prompt %q", prompt)
		}
	})

	t.Run("both", func(t *testing.T) {
		original := []File{base}

		prompt, images, err := EmulateEditGuides("hello", original, &mask, &markup)

		if err != nil {
			t.Fatal(err)
		}

		if len(images) != 3 {
			t.Fatalf("got %d images, want 3", len(images))
		}

		if !strings.Contains(prompt, MaskInstruction) || !strings.Contains(prompt, MarkupReferenceInstruction(2)) {
			t.Errorf("prompt %q", prompt)
		}
		if !bytes.Equal(images[0].Content, base.Content) || bytes.Equal(images[1].Content, base.Content) {
			t.Error("combined guides must preserve the first image and append an annotated reference")
		}

		if !bytes.Equal(original[0].Content, base.Content) {
			t.Error("caller's slice must not be modified")
		}
	})
}

func TestEditGuideValidation(t *testing.T) {
	base := pngFile(t, "base.png", solid(color.White))
	wrongSize := pngFile(t, "mask.png", image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	broken := File{Content: []byte("not an image")}
	for _, tt := range []struct {
		name         string
		images       []File
		mask, markup *File
	}{
		{"missing image", nil, &base, nil},
		{"invalid image", []File{broken}, &base, nil},
		{"invalid mask", []File{base}, &broken, nil},
		{"invalid markup", []File{base}, nil, &broken},
		{"mask dimensions", []File{base}, &wrongSize, nil},
		{"markup dimensions", []File{base}, nil, &wrongSize},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := EmulateEditGuides("edit", tt.images, tt.mask, tt.markup)
			if CodeFromError(err, 500) != 400 {
				t.Fatalf("expected client error, got %v", err)
			}
		})
	}
}
