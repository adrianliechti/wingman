package text

import (
	"regexp"
	"strings"
)

func Normalize(text string) string {
	text = strings.TrimSpace(text)

	// Remove any existing \a characters to prevent interference
	text = strings.ReplaceAll(text, "\a", "")

	// Convert Windows line endings to Unix
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Use \a as temporary marker for paragraph breaks (multiple newlines)
	text = regexp.MustCompile(`\n\s*\n\s*`).ReplaceAllString(text, "\a\a")

	// Use \a as temporary marker for single line breaks
	text = regexp.MustCompile(`\n\s*`).ReplaceAllString(text, "\a")

	// Collapse multiple spaces into single space
	text = strings.Join(strings.Fields(text), " ")

	// Restore line breaks from temporary markers
	text = strings.ReplaceAll(text, "\a", "\n")

	text = strings.TrimSpace(text)

	return text
}
