package toolid

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		value string
		max   int
		want  string
	}{
		{name: "plain id untouched", value: "toolu_01ABC", max: 64, want: "toolu_01ABC"},
		{name: "gemini composite cut at separator", value: "abc123::get_time::c2ln", max: 64, want: "abc123"},
		{name: "invalid runes replaced", value: "functions.Bash:0", max: 64, want: "functions_Bash_0"},
		{name: "truncated to max", value: strings.Repeat("a", 80), max: 64, want: strings.Repeat("a", 64)},
		{name: "leading separator kept as runes", value: "::name", max: 64, want: "__name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.value, tt.max); got != tt.want {
				t.Errorf("Sanitize(%q, %d) = %q, want %q", tt.value, tt.max, got, tt.want)
			}
		})
	}
}
