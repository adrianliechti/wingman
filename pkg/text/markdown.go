package text

import "regexp"

// IsMarkdown detects if text contains markdown formatting
// Uses multiple heuristics to avoid false positives
func IsMarkdown(text string) bool {
	if len(text) == 0 {
		return false
	}

	indicators := 0

	// Check for ATX headings (# Heading)
	if hasATXHeadings(text) {
		indicators++
	}

	// Check for code fences
	if hasCodeFences(text) {
		indicators++
	}

	// Check for markdown lists
	if hasMarkdownLists(text) {
		indicators++
	}

	// Check for links or images
	if hasMarkdownLinks(text) {
		indicators++
	}

	// Check for blockquotes
	if hasBlockquotes(text) {
		indicators++
	}

	// Check for horizontal rules
	if hasHorizontalRules(text) {
		indicators++
	}

	// Require at least 2 different markdown features to avoid false positives
	// This prevents plain text with occasional # or - from being treated as markdown
	return indicators >= 2
}

// Markdown detection patterns

var (
	// ATX headings: # Heading, ## Heading, etc.
	atxHeadingPattern = regexp.MustCompile(`(?m)^#{1,6}\s+.+$`)

	// Code fences: ```lang or ~~~
	codeFencePattern = regexp.MustCompile("(?m)^```|^~~~")

	// Unordered lists: - item, * item, + item
	unorderedListPattern = regexp.MustCompile(`(?m)^[\s]*[-*+]\s+.+$`)

	// Ordered lists: 1. item, 2. item
	orderedListPattern = regexp.MustCompile(`(?m)^[\s]*\d+\.\s+.+$`)

	// Links: [text](url) or images: ![alt](url)
	linkPattern = regexp.MustCompile(`!?\[([^\]]+)\]\(([^)]+)\)`)

	// Blockquotes: > quote
	blockquotePattern = regexp.MustCompile(`(?m)^>\s+.+$`)

	// Horizontal rules: ---, ***, ___
	horizontalRulePattern = regexp.MustCompile(`(?m)^[\s]*(-{3,}|\*{3,}|_{3,})[\s]*$`)
)

func hasATXHeadings(text string) bool {
	return atxHeadingPattern.MatchString(text)
}

func hasCodeFences(text string) bool {
	return codeFencePattern.MatchString(text)
}

func hasMarkdownLists(text string) bool {
	return unorderedListPattern.MatchString(text) || orderedListPattern.MatchString(text)
}

func hasMarkdownLinks(text string) bool {
	return linkPattern.MatchString(text)
}

func hasBlockquotes(text string) bool {
	return blockquotePattern.MatchString(text)
}

func hasHorizontalRules(text string) bool {
	return horizontalRulePattern.MatchString(text)
}
