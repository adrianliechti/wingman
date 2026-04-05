package text

import (
	"testing"
)

func TestNormalize_LineEndings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"unix", "hello\nworld", "hello\nworld"},
		{"windows", "hello\r\nworld", "hello\nworld"},
		{"old mac", "hello\rworld", "hello\nworld"},
		{"mixed", "a\r\nb\rc\nd", "a\nb\nc\nd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_BlankLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"single blank line preserved", "a\n\nb", "a\n\nb"},
		{"triple collapsed to double", "a\n\n\nb", "a\n\nb"},
		{"many collapsed to double", "a\n\n\n\n\n\nb", "a\n\nb"},
		{"multiple groups", "a\n\n\n\nb\n\n\n\nc", "a\n\nb\n\nc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_HorizontalWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"double space", "hello  world", "hello world"},
		{"tabs", "hello\t\tworld", "hello world"},
		{"mixed spaces and tabs", "hello \t  world", "hello world"},
		{"preserves single space", "hello world", "hello world"},
		{"preserves newlines", "hello\n  indented", "hello\n indented"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_Trim(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"leading spaces", "  hello", "hello"},
		{"trailing spaces", "hello  ", "hello"},
		{"leading newlines", "\n\nhello", "hello"},
		{"trailing newlines", "hello\n\n", "hello"},
		{"both sides", "  \n hello \n  ", "hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_Empty(t *testing.T) {
	if got := Normalize(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := Normalize("   \n\n  "); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestNormalize_HTMLEntities(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"amp", "Tom &amp; Jerry", "Tom & Jerry"},
		{"lt gt", "&lt;div&gt;hello&lt;/div&gt;", "<div>hello</div>"},
		{"nbsp", "hello&nbsp;world", "hello world"},
		{"numeric", "&#169; 2024", "\u00A9 2024"},
		{"hex", "&#x00A9; 2024", "\u00A9 2024"},
		{"quot", "&quot;quoted&quot;", "\"quoted\""},
		{"multiple", "a &amp; b &lt; c", "a & b < c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_ZeroWidthChars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"BOM", "\uFEFFhello", "hello"},
		{"zero-width space", "hello\u200Bworld", "helloworld"},
		{"zero-width joiner", "hello\u200Dworld", "helloworld"},
		{"word joiner", "hello\u2060world", "helloworld"},
		{"soft hyphen", "auto\u00ADmatic", "automatic"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.input)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNormalize_NonBreakingSpace(t *testing.T) {
	got := Normalize("hello\u00A0\u00A0world")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestNormalize_Realistic(t *testing.T) {
	input := "  The Project Gutenberg eBook\r\n\r\n\r\n\r\n" +
		"Title:   Siddhartha\r\n\r\n" +
		"Author:    Hermann Hesse\r\n\r\n\r\n" +
		"  Chapter One  \r\n\r\n" +
		"In the shade of the house,  in the sunshine  on the river bank...\r\n"

	got := Normalize(input)

	expected := "The Project Gutenberg eBook\n\n" +
		"Title: Siddhartha\n\n" +
		"Author: Hermann Hesse\n\n" +
		" Chapter One \n\n" +
		"In the shade of the house, in the sunshine on the river bank..."

	if got != expected {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, expected)
	}
}

func TestNormalize_HTMLExtracted(t *testing.T) {
	// Simulates text extracted from an HTML page (e.g. via Tika or scraping)
	input := "\uFEFF\n\n\nWelcome&nbsp;to&nbsp;the\u00A0site\r\n\r\n\r\n\r\n" +
		"Products &amp; Services\r\n\r\n" +
		"Contact\u200B us  \t  today!\r\n"

	got := Normalize(input)

	expected := "Welcome to the site\n\n" +
		"Products & Services\n\n" +
		"Contact us today!"

	if got != expected {
		t.Errorf("got:\n%q\n\nwant:\n%q", got, expected)
	}
}
