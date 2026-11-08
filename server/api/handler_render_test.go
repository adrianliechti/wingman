package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type recordingRenderer struct {
	input   string
	options *provider.RenderOptions
}

func (r *recordingRenderer) Render(_ context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	r.input, r.options = input, options
	return &provider.Rendering{Content: []byte("png"), ContentType: "image/png"}, nil
}

func TestRenderSeparatesGuidesFromImages(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)

	_ = form.WriteField("input", "add a sun")

	for field, content := range map[string]string{"file": "scene", "mask": "alpha", "markup": "ring"} {
		part, err := form.CreateFormFile(field, field+".png")

		if err != nil {
			t.Fatal(err)
		}

		_, _ = part.Write([]byte(content))
	}

	form.Close()

	r := httptest.NewRequest(http.MethodPost, "/v1/render", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())

	p := &recordingRenderer{}

	cfg := &config.Config{Policy: translatePolicy{}}
	cfg.RegisterRenderer("", p)

	w := httptest.NewRecorder()
	New(cfg).handleRender(w, r)

	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}

	o := p.options

	if p.input != "add a sun" || len(o.Images) != 1 || string(o.Images[0].Content) != "scene" {
		t.Fatalf("images = %+v, input %q", o.Images, p.input)
	}

	if o.Mask == nil || string(o.Mask.Content) != "alpha" || o.Markup == nil || string(o.Markup.Content) != "ring" {
		t.Errorf("mask = %+v, markup = %+v", o.Mask, o.Markup)
	}
}

func TestRenderDurationUnits(t *testing.T) {
	for _, test := range []struct {
		field, value string
		valid        bool
	}{
		{"duration_seconds", "15", true}, {"frame_duration_ms", "200", true},
		{"duration_seconds", "0", false}, {"duration_seconds", "-1", false},
		{"duration_seconds", "1.5", false}, {"duration_seconds", "9223372036854775807", false},
		{"frame_duration_ms", "9223372036854775807", false}, {"frame_duration_ms", "-1", false},
	} {
		t.Run(test.field+"="+test.value, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			form.WriteField("input", "fox")
			form.WriteField(test.field, test.value)
			form.Close()
			request := httptest.NewRequest(http.MethodPost, "/render", &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			request.Header.Set("Accept", "video/mp4")
			renderer := &recordingRenderer{}
			cfg := &config.Config{Policy: translatePolicy{}}
			cfg.RegisterRenderer("", renderer)
			response := httptest.NewRecorder()
			New(cfg).handleRender(response, request)
			if !test.valid {
				if response.Code != http.StatusBadRequest || renderer.options != nil {
					t.Fatalf("response=%d, options=%+v", response.Code, renderer.options)
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("response=%s", response.Body)
			}
			if renderer.options.Format != provider.RenderFormatMP4 {
				t.Errorf("format=%s", renderer.options.Format)
			}
			if test.field == "duration_seconds" && (renderer.options.Duration == nil || *renderer.options.Duration != 15*time.Second) {
				t.Errorf("duration=%v", renderer.options.Duration)
			}
			if test.field == "frame_duration_ms" && (renderer.options.FrameDuration == nil || *renderer.options.FrameDuration != 200*time.Millisecond) {
				t.Errorf("frame duration=%v", renderer.options.FrameDuration)
			}
		})
	}
}
