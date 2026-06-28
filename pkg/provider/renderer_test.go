package provider

import "testing"

func TestParseSizeAspect(t *testing.T) {
	tests := map[string]AspectRatio{
		"1024x1024": AspectRatio1x1,
		"1536x1024": AspectRatio3x2,
		"1024x1536": AspectRatio2x3,
		"1792x1024": "7:4",
		"auto":      "",
		"":          "",
		"1024":      "",
		"axb":       "",
		"0x100":     "",
	}

	for input, want := range tests {
		if got := ParseSizeAspect(input); got != want {
			t.Errorf("ParseSizeAspect(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseAspect(t *testing.T) {
	tests := map[string]AspectRatio{
		"16:9": AspectRatio16x9,
		"1:1":  AspectRatio1x1,
		"bad":  "",
		"0:1":  "",
		"16:0": "",
		"":     "",
	}

	for input, want := range tests {
		if got := ParseAspect(input); got != want {
			t.Errorf("ParseAspect(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAspectNearest(t *testing.T) {
	gptImage := []AspectRatio{AspectRatio1x1, AspectRatio3x2, AspectRatio2x3}
	dalle3 := []AspectRatio{AspectRatio1x1, AspectRatio16x9, AspectRatio9x16}

	tests := []struct {
		aspect    AspectRatio
		supported []AspectRatio
		want      AspectRatio
	}{
		{AspectRatio16x9, gptImage, AspectRatio3x2},
		{AspectRatio9x16, gptImage, AspectRatio2x3},
		{"7:4", gptImage, AspectRatio3x2},
		{AspectRatio1x1, gptImage, AspectRatio1x1},
		{AspectRatio16x9, dalle3, AspectRatio16x9},
		{"7:4", dalle3, AspectRatio16x9},
	}

	for _, tt := range tests {
		if got := tt.aspect.Nearest(tt.supported); got != tt.want {
			t.Errorf("%q.Nearest(%v) = %q, want %q", tt.aspect, tt.supported, got, tt.want)
		}
	}
}
