package openai

import (
	"bytes"
	"context"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/internal/testimage"

	"github.com/joho/godotenv"
)

// Opt in to paid, network-dependent edit checks with:
// WINGMAN_OPENAI_LIVE=1 go test -v ./pkg/provider/openai -run TestRender_Live -count=1
// WINGMAN_LIVE_CAPTURE names a directory that receives the resulting images.
func TestRender_LiveEditGuides(t *testing.T) {
	if os.Getenv("WINGMAN_OPENAI_LIVE") != "1" {
		t.Skip("set WINGMAN_OPENAI_LIVE=1 to call the real Images API")
	}

	key := os.Getenv("OPENAI_API_KEY")

	if key == "" {
		values, _ := godotenv.Read("../../../.env")
		key = values["OPENAI_API_KEY"]
	}

	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}

	scene := provider.File{Name: "scene.png", Content: testimage.Scene(), ContentType: "image/png"}
	mask := provider.File{Name: "mask.png", Content: testimage.Mask(), ContentType: "image/png"}
	markup := provider.File{Name: "markup.png", Content: testimage.Markup(), ContentType: "image/png"}

	tests := []struct {
		name   string
		model  string
		prompt string
		images []provider.File
		mask   *provider.File
		markup *provider.File
	}{
		{"generate", "gpt-image-2.5-flare", "Draw a yellow sun over a green meadow.", nil, nil, nil},
		{"mask native", "gpt-image-2.5-flare", "Add a bright yellow sun.", []provider.File{scene}, &mask, nil},
		{"markup composited", "gpt-image-2", "Add a colorful hot-air balloon at the marked position.", []provider.File{scene}, nil, &markup},
		{"mask and markup", "gpt-image-2.5-flare", "Add a bright yellow sun inside the marked region.", []provider.File{scene}, &mask, &markup},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewRenderer("", tt.model, WithToken(key))

			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			result, err := r.Render(ctx, tt.prompt, &provider.RenderOptions{
				Images: tt.images,
				Mask:   tt.mask,
				Markup: tt.markup,

				Aspect:  provider.AspectRatio1x1,
				Quality: provider.QualityLow,
			})

			if err != nil {
				t.Fatal(err)
			}

			config, _, err := image.DecodeConfig(bytes.NewReader(result.Content))
			if err != nil || config.Width == 0 || config.Height == 0 {
				t.Fatalf("invalid image output: %v", err)
			}
			if detected := http.DetectContentType(result.Content); detected != result.ContentType {
				t.Fatalf("content type %q, detected %q", result.ContentType, detected)
			}

			t.Logf("%s: %d bytes %s", tt.model, len(result.Content), result.ContentType)

			if dir := os.Getenv("WINGMAN_LIVE_CAPTURE"); dir != "" {
				name := filepath.Join(dir, "openai-"+tt.model+"-"+filepath.Base(tt.name)+".png")
				if err := os.WriteFile(name, result.Content, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
