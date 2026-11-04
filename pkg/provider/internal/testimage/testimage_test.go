package testimage

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestImages checks the scenes decode at the expected size and, when
// WINGMAN_LIVE_CAPTURE names a directory, writes them there for inspection.
func TestImages(t *testing.T) {
	images := map[string][]byte{
		"scene.png":  Scene(),
		"mask.png":   Mask(),
		"markup.png": Markup(),
	}

	for name, data := range images {
		img, err := png.Decode(bytes.NewReader(data))

		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if img.Bounds().Dx() != Size || img.Bounds().Dy() != Size {
			t.Errorf("%s: %v, want %dx%d", name, img.Bounds().Size(), Size, Size)
		}

		if dir := os.Getenv("WINGMAN_LIVE_CAPTURE"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
		}
	}
}
