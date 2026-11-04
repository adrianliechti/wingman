package server_test

import (
	"context"
	"encoding/json/v2"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"

	"github.com/joho/godotenv"
)

// Uses a nonexistent model to compare real upstream failures with the errors
// returned through all public image routes. No images should be generated.
func TestImagesLiveErrors(t *testing.T) {
	for _, backend := range []string{"openai", "google"} {
		t.Run(backend, func(t *testing.T) {
			if os.Getenv("WINGMAN_"+strings.ToUpper(backend)+"_LIVE") != "1" {
				t.Skip("enable the provider's WINGMAN_*_LIVE flag to check live errors")
			}
			keyName := "OPENAI_API_KEY"
			if backend == "google" {
				keyName = "GEMINI_API_KEY"
			}
			key := os.Getenv(keyName)
			if key == "" {
				values, _ := godotenv.Read("../.env")
				key = values[keyName]
			}
			if key == "" {
				t.Fatalf("%s is required", keyName)
			}
			const model = "wingman-image-review-nonexistent-model"
			var renderer provider.Renderer
			var err error
			if backend == "openai" {
				renderer, err = openai.NewRenderer("", model, openai.WithToken(key), openai.WithMaxRetries(0))
			} else {
				renderer, err = google.NewRenderer(model, google.WithToken(key))
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterRenderer("image-test", renderer)
			s, err := server.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			base := imagePNG(t, 1024, false)
			for _, endpoint := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/render"} {
				t.Run(endpoint, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					options := &provider.RenderOptions{}
					var uploads []imageUpload
					if endpoint == "/v1/images/edits" {
						options.Images = []provider.File{{Name: "image.png", Content: base, ContentType: "image/png"}}
						uploads = []imageUpload{{"image", base}}
					}
					_, directErr := renderer.Render(ctx, "draw a sun", options)
					if directErr == nil {
						t.Fatal("nonexistent model unexpectedly succeeded")
					}
					wantStatus := provider.CodeFromError(directErr, 0)
					if wantStatus < 400 {
						t.Fatalf("expected a typed provider error, got %v", directErr)
					}
					req := imageRequest(t, endpoint, map[string]string{"model": "image-test", "prompt": "draw a sun"}, uploads).WithContext(ctx)
					w := httptest.NewRecorder()
					s.ServeHTTP(w, req)
					if w.Code != wantStatus {
						t.Fatalf("proxy status=%d direct=%d body=%s", w.Code, wantStatus, w.Body.String())
					}
					message := w.Body.String()
					if endpoint != "/v1/render" {
						var response struct {
							Error struct {
								Code    string `json:"code"`
								Message string `json:"message"`
							} `json:"error"`
						}
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						message = response.Error.Message
						if response.Error.Code != provider.TypeFromError(directErr) {
							t.Errorf("proxy error code=%q direct=%q", response.Error.Code, provider.TypeFromError(directErr))
						}
						if w.Header().Get("Content-Type") != "application/json" {
							t.Error("missing JSON content type")
						}
					}
					if message != directErr.Error() {
						t.Errorf("proxy message=%q direct=%q", message, directErr.Error())
					}
					t.Logf("direct and proxy agree: HTTP %d, code %q", wantStatus, provider.TypeFromError(directErr))
				})
			}
		})
	}
}
