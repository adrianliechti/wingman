package text

import (
	"html"
	"regexp"
	"strings"
)

var (
	reWindowsNewline   = regexp.MustCompile(`\r\n`)
	reCarriageReturn   = regexp.MustCompile(`\r`)
	reBlankLines       = regexp.MustCompile(`\n{3,}`)
	reHorizontalSpaces = regexp.MustCompile(`[^\S\n]{2,}`)
)

// zeroWidthChars are invisible Unicode characters commonly found in web-extracted text.
var zeroWidthChars = strings.NewReplacer(
	"\uFEFF", "", // BOM / zero-width no-break space
	"\u200B", "", // zero-width space
	"\u200C", "", // zero-width non-joiner
	"\u200D", "", // zero-width joiner
	"\u2060", "", // word joiner
	"\u00AD", "", // soft hyphen
)

// Normalize cleans text for downstream processing (splitting, indexing, search).
// It handles common artifacts from HTML/PDF extraction and web scraping:
//   - Decodes HTML entities (&amp; &lt; &gt; &nbsp; &#160; etc.)
//   - Removes BOM and zero-width Unicode characters
//   - Replaces non-breaking spaces (U+00A0) with regular spaces
//   - Normalizes line endings to \n
//   - Collapses excessive blank lines to a single blank line
//   - Collapses runs of horizontal whitespace to a single space
func Normalize(text string) string {
	// Decode HTML entities (handles &amp; &lt; &gt; &nbsp; &#160; &#x00A0; etc.)
	text = html.UnescapeString(text)

	// Remove BOM and zero-width characters
	text = zeroWidthChars.Replace(text)

	// Replace non-breaking spaces with regular spaces
	text = strings.ReplaceAll(text, "\u00A0", " ")

	text = strings.TrimSpace(text)

	// Normalize line endings to \n
	text = reWindowsNewline.ReplaceAllString(text, "\n")
	text = reCarriageReturn.ReplaceAllString(text, "\n")

	// Collapse 3+ consecutive newlines to 2 (preserves paragraph breaks)
	text = reBlankLines.ReplaceAllString(text, "\n\n")

	// Collapse runs of horizontal whitespace (spaces/tabs) to a single space
	text = reHorizontalSpaces.ReplaceAllString(text, " ")

	text = strings.TrimSpace(text)

	return text
}
