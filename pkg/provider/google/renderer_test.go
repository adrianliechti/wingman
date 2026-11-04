package google

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestRendererAspects(t *testing.T) {
	tests := []struct {
		model  string
		aspect provider.AspectRatio
		want   string
	}{
		{"gemini-nano-banana-2.1", provider.AspectRatio1x4, "1:4"},
		{"gemini-3.1-flash-image", provider.AspectRatio8x1, "8:1"},
		{"gemini-3.1-flash-lite-image", provider.AspectRatio16x9, "16:9"},
		{"gemini-3-pro-image", provider.AspectRatio1x4, "9:16"},
		{"gemini-3-pro-image-preview", provider.AspectRatio8x1, "21:9"},
		{"gemini-2.5-flash-image", provider.AspectRatio4x1, "21:9"},
		{"gemini-2.5-flash-image", "", ""},
		{"custom-image-model", provider.AspectRatio8x1, "21:9"},
	}

	for _, tt := range tests {
		r := &Renderer{Config: &Config{model: tt.model}}

		config := imageConfig(&provider.RenderOptions{Aspect: tt.aspect}, r.aspects())

		var got string

		if config.ImageConfig != nil {
			got = config.ImageConfig.AspectRatio
		}
		if len(config.ResponseModalities) != 1 || config.ResponseModalities[0] != "IMAGE" {
			t.Errorf("%s: must explicitly request image output", tt.model)
		}

		if got != tt.want {
			t.Errorf("%s aspect=%q: got %q, want %q", tt.model, tt.aspect, got, tt.want)
		}
	}
}
