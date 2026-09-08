package openai

import (
	"strconv"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestRendererSizeFor(t *testing.T) {
	tests := []struct {
		model      string
		aspect     provider.AspectRatio
		resolution provider.Resolution
		want       string
	}{
		{"gpt-image-1", "", "", ""},
		{"gpt-image-1", provider.AspectRatio16x9, "", "1536x1024"},
		{"gpt-image-1-mini", provider.AspectRatio2x3, provider.Resolution4K, "1024x1536"},

		{"gpt-image-2", provider.AspectRatio16x9, "", "1536x864"},
		{"gpt-image-2", provider.AspectRatio21x9, provider.Resolution4K, "1536x864"},

		{"gpt-image-2.5-flare", "", "", ""},
		{"gpt-image-2.5-flare", provider.AspectRatio1x1, "", "1024x1024"},
		{"gpt-image-2.5-flare", provider.AspectRatio16x9, "", "1536x864"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio4x3, "", "1456x1088"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio1x1, provider.Resolution1K, "1248x1248"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio16x9, provider.Resolution2K, "2560x1440"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio16x9, provider.Resolution4K, "3840x2160"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio1x1, provider.Resolution4K, "2880x2880"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio21x9, provider.Resolution4K, "3840x1648"},
		{"gpt-image-2.5-sunburst", provider.AspectRatio9x16, provider.Resolution512, "672x1184"},
		{"gpt-image-2.5-sunburst", "", provider.Resolution2K, "1920x1920"},
	}

	for _, tt := range tests {
		r := &Renderer{Config: &Config{model: tt.model}}

		got := r.sizeFor(tt.aspect, tt.resolution)

		if got != tt.want {
			t.Errorf("%s aspect=%q resolution=%q: got %q, want %q", tt.model, tt.aspect, tt.resolution, got, tt.want)
		}

		if got != "" && r.supportsCustomSize() {
			assertCustomSize(t, got)
		}
	}
}

func assertCustomSize(t *testing.T, size string) {
	t.Helper()

	parts := strings.SplitN(size, "x", 2)

	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])

	if w%customSizeStep != 0 || h%customSizeStep != 0 {
		t.Errorf("%s: edges must be multiples of %d", size, customSizeStep)
	}

	if w > customSizeEdge || h > customSizeEdge {
		t.Errorf("%s: edge exceeds %d", size, customSizeEdge)
	}

	if px := w * h; px < customSizeMinPx || px > customSizeMaxPx {
		t.Errorf("%s: %d pixels outside %d..%d", size, px, customSizeMinPx, customSizeMaxPx)
	}

	if ratio := float64(w) / float64(h); ratio < customAspectMin-0.01 || ratio > customAspectMax+0.01 {
		t.Errorf("%s: aspect %.2f outside 1:3..3:1", size, ratio)
	}
}

func TestCustomSizeAllAspects(t *testing.T) {
	aspects := []provider.AspectRatio{
		provider.AspectRatio1x1,
		provider.AspectRatio2x3,
		provider.AspectRatio3x2,
		provider.AspectRatio3x4,
		provider.AspectRatio4x3,
		provider.AspectRatio4x5,
		provider.AspectRatio5x4,
		provider.AspectRatio9x16,
		provider.AspectRatio16x9,
		provider.AspectRatio21x9,
		"3:1",
		"1:3",
		"4:1",
	}

	resolutions := []provider.Resolution{
		"",
		provider.Resolution512,
		provider.Resolution1K,
		provider.Resolution2K,
		provider.Resolution4K,
	}

	for _, aspect := range aspects {
		for _, resolution := range resolutions {
			size := customSize(aspect, resolution)

			if size == "" {
				t.Errorf("aspect=%q resolution=%q: empty size", aspect, resolution)
				continue
			}

			assertCustomSize(t, size)
		}
	}
}

func TestRendererQualityFor(t *testing.T) {
	tests := []struct {
		model   string
		quality provider.Quality
		want    string
	}{
		{"gpt-image-1", "", ""},
		{"gpt-image-1", provider.QualityHigh, "high"},
		{"gpt-image-1", provider.QualityXHigh, "high"},
		{"gpt-image-2", provider.QualityMax, "high"},
		{"gpt-image-2.5-flare", provider.QualityLow, "low"},
		{"gpt-image-2.5-flare", provider.QualityXHigh, "xhigh"},
		{"gpt-image-2.5-sunburst", provider.QualityMax, "max"},
	}

	for _, tt := range tests {
		r := &Renderer{Config: &Config{model: tt.model}}

		if got := r.qualityFor(tt.quality); got != tt.want {
			t.Errorf("%s quality=%q: got %q, want %q", tt.model, tt.quality, got, tt.want)
		}
	}
}

func TestRendererSupportsTransparent(t *testing.T) {
	tests := map[string]bool{
		"gpt-image-1":            true,
		"gpt-image-1-mini":       true,
		"gpt-image-2":            false,
		"gpt-image-2.5-flare":    true,
		"GPT-Image-2.5-Sunburst": true,
	}

	for model, want := range tests {
		r := &Renderer{Config: &Config{model: model}}

		if got := r.supportsTransparent(); got != want {
			t.Errorf("%s: got %v, want %v", model, got, want)
		}
	}
}
