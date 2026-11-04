package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

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
