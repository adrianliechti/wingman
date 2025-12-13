package text

import (
	"testing"
)

func TestAutoSplitterDetectsMarkdown(t *testing.T) {
	testCases := []struct {
		name       string
		text       string
		isMarkdown bool
	}{
		{
			name: "heading and list",
			text: `# Title

- Item 1
- Item 2`,
			isMarkdown: true,
		},
		{
			name:       "heading and code fence",
			text:       "# Code\n\n```go\nfunc main() {}\n```",
			isMarkdown: true,
		},
		{
			name:       "list and links",
			text:       "- [Link 1](http://example.com)\n- [Link 2](http://example.org)",
			isMarkdown: true,
		},
		{
			name:       "blockquote and heading",
			text:       "# Quote\n\n> This is a quote",
			isMarkdown: true,
		},
		{
			name:       "plain text",
			text:       "This is just plain text without any markdown.",
			isMarkdown: false,
		},
		{
			name:       "plain text with hash",
			text:       "Use #hashtag for social media",
			isMarkdown: false,
		},
		{
			name:       "plain text with dash",
			text:       "Buy some items at the store - milk, bread, eggs",
			isMarkdown: false,
		},
		{
			name:       "empty text",
			text:       "",
			isMarkdown: false,
		},
		{
			name:       "only whitespace",
			text:       "   \n\n   ",
			isMarkdown: false,
		},
		{
			name: "complex markdown",
			text: `# Documentation

## Overview

This is a **complex** document with:

- Lists
- Code blocks

` + "```python\nprint('hello')\n```" + `

### Links

See [docs](http://example.com).`,
			isMarkdown: true,
		},
		{
			name:       "horizontal rule and heading",
			text:       "# Section 1\n\n---\n\n# Section 2",
			isMarkdown: true,
		},
		{
			name:       "ordered list and headings",
			text:       "# Steps\n\n1. First step\n2. Second step\n3. Third step",
			isMarkdown: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			detected := IsMarkdown(tc.text)

			if detected != tc.isMarkdown {
				t.Errorf("Expected isMarkdown=%v, got %v for text: %q", tc.isMarkdown, detected, tc.text)
			}
		})
	}
}

func TestAutoSplitterUsesCorrectSplitter(t *testing.T) {
	markdown := `# Title

Content with **formatting**.

## Section

- Item 1
- Item 2`

	plainText := `This is plain text.
It has multiple sentences.
But no markdown formatting.`

	t.Run("markdown content", func(t *testing.T) {
		splitter := NewAutoSplitter()
		splitter.ChunkSize = 50

		chunks := splitter.Split(markdown)

		if len(chunks) == 0 {
			t.Fatal("Expected chunks for markdown")
		}

		// Markdown splitter should handle this
		t.Logf("Markdown chunks: %v", chunks)
	})

	t.Run("plain text content", func(t *testing.T) {
		splitter := NewAutoSplitter()
		splitter.ChunkSize = 50

		chunks := splitter.Split(plainText)

		if len(chunks) == 0 {
			t.Fatal("Expected chunks for plain text")
		}

		// Text splitter should handle this
		t.Logf("Plain text chunks: %v", chunks)
	})
}

func TestAutoSplitterChunkSize(t *testing.T) {
	splitter := NewAutoSplitter()
	splitter.ChunkSize = 30

	markdown := `# Long Title

This is a long paragraph that should be split across multiple chunks because it exceeds the chunk size limit.

## Another Section

More content here.`

	chunks := splitter.Split(markdown)

	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds size: %d > %d", i, size, splitter.ChunkSize)
		}
	}
}

func TestAutoSplitterWithOverlap(t *testing.T) {
	splitter := NewAutoSplitter()
	splitter.ChunkSize = 50
	splitter.ChunkOverlap = 10

	markdown := `# Section 1

Content for section 1.

# Section 2

Content for section 2.`

	chunks := splitter.Split(markdown)

	if len(chunks) < 2 {
		t.Fatal("Expected multiple chunks")
	}

	t.Logf("Chunks with overlap: %v", chunks)
}

func TestAutoSplitterEmptyText(t *testing.T) {
	splitter := NewAutoSplitter()
	chunks := splitter.Split("")

	if len(chunks) != 0 {
		t.Errorf("Expected no chunks for empty text, got %d", len(chunks))
	}
}

func TestDetectMarkdownHelper(t *testing.T) {
	testCases := []struct {
		text       string
		isMarkdown bool
	}{
		{"# Heading\n\n- List", true},
		{"Plain text", false},
		{"# Just one heading", false}, // Only one feature, not enough
		{"```code```\n\n# Heading", true},
	}

	for _, tc := range testCases {
		result := IsMarkdown(tc.text)
		if result != tc.isMarkdown {
			t.Errorf("IsMarkdown(%q) = %v, want %v", tc.text, result, tc.isMarkdown)
		}
	}
}

func TestAutoSplitterPreservesContent(t *testing.T) {
	splitter := NewAutoSplitter()
	splitter.ChunkSize = 100

	text := `# Important Document

This contains **critical** information.

## Details

More important details here.`

	chunks := splitter.Split(text)

	// Verify important content is preserved
	found := false
	for _, chunk := range chunks {
		if containsAny(chunk, "Important", "critical", "Details") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Lost important content during chunking")
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
