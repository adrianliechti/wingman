package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/xml"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// WINGMAN_OPENAI_LIVE=1 enables paid requests through Wingman's HTTP API.
// WINGMAN_LIVE_CAPTURE optionally saves the generated alpaca GIF and SVG.
func TestImagesLiveGPTImage26(t *testing.T) {
	if os.Getenv("WINGMAN_OPENAI_LIVE") != "1" {
		t.Skip("set WINGMAN_OPENAI_LIVE=1 to generate images with the real API")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		values, _ := godotenv.Read("../.env")
		key = values["OPENAI_API_KEY"]
	}
	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}
	const prompt = "A cute fluffy cream-colored alpaca wearing a tiny teal scarf, jumping joyfully. " +
		"Clean 2D vector illustration, rounded shapes, an expressive happy face, full body centered, " +
		"plain pale peach background, no text. "
	for _, tt := range []struct{ model, format, action string }{
		{"gpt-image-2.6-flare", "gif", "Animate a seamless loop: crouch, jump up, land softly, and return to the starting pose. Keep the camera still."},
		{"gpt-image-2.6-sunburst", "svg", "Show one playful midair jumping pose with all four hooves off the ground and a small oval shadow below."},
	} {
		t.Run(tt.model+"/"+tt.format, func(t *testing.T) {
			renderer, err := openai.NewRenderer("", tt.model, openai.WithToken(key), openai.WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Policy: noop.New()}
			cfg.RegisterRenderer(tt.model, renderer)
			handler, err := server.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			local := httptest.NewServer(handler)
			defer local.Close()
			fields := map[string]any{
				"model": tt.model, "prompt": prompt + tt.action,
				"output_format": tt.format, "aspect_ratio": "1:1",
				"quality": "medium", "complexity": "medium",
			}
			if tt.format == "gif" {
				fields["frame_count"], fields["frame_duration_ms"] = 9, 200
			} else {
				fields["svg"] = map[string]string{"model": "gpt-6.1-sol"}
			}
			body, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, local.URL+"/v1/images/generations", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := local.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var response struct {
				Data []struct {
					Base64 string `json:"b64_json"`
				} `json:"data"`
				Error any `json:"error"`
			}
			if err := json.UnmarshalRead(resp.Body, &response); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || len(response.Data) != 1 {
				t.Fatalf("HTTP %d: %v", resp.StatusCode, response.Error)
			}
			data, err := base64.StdEncoding.DecodeString(response.Data[0].Base64)
			if err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("WINGMAN_LIVE_CAPTURE"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(dir, "jumping-alpaca-"+tt.model+"."+tt.format)
				if err := os.WriteFile(name, data, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("saved %s (%d bytes)", name, len(data))
			}
			if tt.format == "gif" {
				animation, err := gif.DecodeAll(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				if len(animation.Image) != 9 || animation.Config.Width != 384 || animation.Config.Height != 384 {
					t.Fatalf("GIF: %d frames, %dx%d; want 9 frames at 384x384", len(animation.Image), animation.Config.Width, animation.Config.Height)
				}
				for _, delay := range animation.Delay {
					if delay != 20 {
						t.Errorf("GIF frame delay = %d centiseconds, want 20", delay)
					}
				}
				t.Logf("validated GIF: 9 frames, 384x384, 200ms/frame, loop count %d", animation.LoopCount)
			} else {
				var document struct{ XMLName xml.Name }
				if err := xml.Unmarshal(data, &document); err != nil {
					t.Fatal(err)
				}
				if document.XMLName.Local != "svg" || document.XMLName.Space != "http://www.w3.org/2000/svg" {
					t.Fatalf("invalid SVG root: %+v", document.XMLName)
				}
				t.Logf("validated SVG XML (%d bytes)", len(data))
			}
		})
	}
}

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
