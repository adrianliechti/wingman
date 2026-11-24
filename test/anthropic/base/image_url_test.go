package base_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
)

// TestImageURLSourceHTTP sends an image block with a URL source. Wingman must
// fetch the image itself so URL sources work across backends without native
// URL support — which also means a local URL suffices. Wingman-only: the
// reference API would have to fetch the URL from its side.
func TestImageURLSourceHTTP(t *testing.T) {
	h := anthropic.New(t)

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(buf.Bytes())
	}))
	defer server.Close()

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.RequireConfigured(t, model.Name)

			body := map[string]any{
				"model":      model.Name,
				"max_tokens": 200,
				"messages": []map[string]any{
					{
						"role": "user",
						"content": []map[string]any{
							{
								"type":   "image",
								"source": map[string]any{"type": "url", "url": server.URL + "/square.png"},
							},
							{
								"type": "text",
								"text": "What is the dominant color of this image? Reply with a single word.",
							},
						},
					},
				},
			}

			resp := anthropic.PostMessages(t, h, h.Wingman, body)

			if resp.StatusCode != 200 {
				t.Fatalf("wingman returned status %d: %s", resp.StatusCode, string(resp.RawBody))
			}

			requireDocumentAnswered(t, "wingman", resp.Body, "red")
		})
	}
}
