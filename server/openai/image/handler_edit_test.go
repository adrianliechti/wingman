package image

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type openPolicy struct{}

func (openPolicy) Verify(context.Context, policy.Resource, string, policy.Action) error {
	return nil
}

type recordingRenderer struct {
	input   string
	options *provider.RenderOptions
}

func (r *recordingRenderer) Render(_ context.Context, input string, options *provider.RenderOptions) (*provider.Rendering, error) {
	r.input, r.options = input, options
	return &provider.Rendering{Content: []byte("png"), ContentType: "image/png"}, nil
}

func editUpload(t *testing.T, files map[string]string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)

	_ = form.WriteField("model", "test")
	_ = form.WriteField("prompt", "add a sun")

	for field, content := range files {
		part, err := form.CreateFormFile(field, field+".png")

		if err != nil {
			t.Fatal(err)
		}

		_, _ = part.Write([]byte(content))
	}

	form.Close()

	r := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())

	return r
}

func TestImageEditPassesMaskAndMarkup(t *testing.T) {
	p := &recordingRenderer{}

	cfg := &config.Config{Policy: openPolicy{}}
	cfg.RegisterRenderer("test", p)

	w := httptest.NewRecorder()
	New(cfg).handleImageEdit(w, editUpload(t, map[string]string{"image": "scene", "mask": "alpha", "markup": "ring"}))

	if w.Code != http.StatusOK {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}

	o := p.options

	if p.input != "add a sun" || o == nil || len(o.Images) != 1 || string(o.Images[0].Content) != "scene" {
		t.Fatalf("images = %+v, input %q", o, p.input)
	}

	if o.Mask == nil || string(o.Mask.Content) != "alpha" || o.Mask.Name != "mask.png" {
		t.Errorf("mask = %+v", o.Mask)
	}

	if o.Markup == nil || string(o.Markup.Content) != "ring" {
		t.Errorf("markup = %+v", o.Markup)
	}
}

func TestImageEditWithoutGuides(t *testing.T) {
	p := &recordingRenderer{}

	cfg := &config.Config{Policy: openPolicy{}}
	cfg.RegisterRenderer("test", p)

	w := httptest.NewRecorder()
	New(cfg).handleImageEdit(w, editUpload(t, map[string]string{"image[]": "scene"}))

	if w.Code != http.StatusOK {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}

	if o := p.options; len(o.Images) != 1 || o.Mask != nil || o.Markup != nil {
		t.Errorf("options = %+v", o)
	}
}
