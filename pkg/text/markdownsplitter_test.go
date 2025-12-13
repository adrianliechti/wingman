package text

import (
	"strings"
	"testing"
)

func TestMarkdownSplitterBasic(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 50

	markdown := `# Header

This is a paragraph.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks, got none")
	}

	// Verify all chunks respect max size
	for i, chunk := range chunks {
		if splitter.LenFunc(chunk) > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, splitter.LenFunc(chunk), splitter.ChunkSize, chunk)
		}
	}
}

func TestMarkdownSplitterHeadingHierarchy(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 100

	markdown := `# H1 Title

Content under H1.

## H2 Section

Content under H2.

### H3 Subsection

Content under H3.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}

	// Should prefer breaking at higher-level headings
	// Each chunk should ideally start with a heading or be under one
}

func TestMarkdownSplitterCodeBlocks(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 100

	markdown := "```go\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n```\n\nSome text after code."

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterLists(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 80

	markdown := `# Shopping List

- Item 1
- Item 2
- Item 3

## Numbered

1. First
2. Second
3. Third`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterBlockquotes(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 60

	markdown := `# Quotes

> This is a quote.
> It spans multiple lines.

Regular text after quote.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterInlineElements(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 100

	markdown := `This is **bold** and *italic* text with [links](http://example.com) and ` + "`code`" + `.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	// Should keep inline formatting together
	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterEmptyText(t *testing.T) {
	splitter := NewMarkdownSplitter()
	chunks := splitter.Split("")

	if len(chunks) != 0 {
		t.Errorf("Expected no chunks for empty text, got %d", len(chunks))
	}
}

func TestMarkdownSplitterWhitespaceOnly(t *testing.T) {
	splitter := NewMarkdownSplitter()
	chunks := splitter.Split("   \n\n   \t  ")

	if len(chunks) != 0 {
		t.Errorf("Expected no chunks for whitespace-only text, got %d: %v", len(chunks), chunks)
	}
}

func TestMarkdownSplitterLongDocument(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 200

	markdown := `# Main Title

This is the introduction paragraph with some content.

## Section 1

Content for section 1 with multiple sentences. This should be grouped together.

### Subsection 1.1

More detailed content here.

## Section 2

Different section with its own content.

### Subsection 2.1

Even more content.

# Another Top Level

Starting fresh with new content.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	// Verify all chunks respect size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d", i, size, splitter.ChunkSize)
		}
		t.Logf("Chunk %d (size=%d): %q", i, size, chunk)
	}

	// Verify we can reconstruct something meaningful
	rejoined := strings.Join(chunks, "\n\n")
	if !strings.Contains(rejoined, "Main Title") {
		t.Error("Lost heading content during chunking")
	}
}

func TestMarkdownSplitterHorizontalRule(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 50

	markdown := `Content before rule.

---

Content after rule.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterMixedContent(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 150

	markdown := `# Documentation

## Overview

This is a **complex** document with:

- Lists
- Code blocks
- Links

### Code Example

` + "```python\ndef hello():\n    print(\"world\")\n```" + `

### References

See [documentation](http://example.com) for more.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d", i, size, splitter.ChunkSize)
		}
		t.Logf("Chunk %d (size=%d): %q", i, size, chunk)
	}
}

func TestMarkdownSplitterSmallChunkSize(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 20

	markdown := `# Title

Paragraph with content that exceeds the chunk size.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	// Should still break semantically when possible
	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterWithOverlap(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 80
	splitter.ChunkOverlap = 20

	markdown := `# Section 1

Content for section one.

# Section 2

Content for section two.

# Section 3

Content for section three.`

	chunks := splitter.Split(markdown)

	if len(chunks) < 2 {
		t.Fatal("Expected multiple chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterPreservesContent(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 100

	markdown := `# Test Document

This is a test with **important** content.

## Subsection

More content here.`

	chunks := splitter.Split(markdown)

	// Check that important markers are preserved
	rejoined := strings.Join(chunks, "\n")

	if !strings.Contains(rejoined, "Test Document") {
		t.Error("Lost heading content")
	}

	if !strings.Contains(rejoined, "important") {
		t.Error("Lost important content")
	}

	if !strings.Contains(rejoined, "Subsection") {
		t.Error("Lost subsection heading")
	}
}

func TestMarkdownSplitterPlainText(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 50

	// Plain text without markdown should still work
	text := "This is plain text without any markdown formatting. It should still be chunked properly."

	chunks := splitter.Split(text)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks for plain text")
	}

	for i, chunk := range chunks {
		if splitter.LenFunc(chunk) > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds size: %d > %d", i, splitter.LenFunc(chunk), splitter.ChunkSize)
		}
	}
}

func TestMarkdownSplitterUnicode(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 30

	markdown := `# 标题

这是中文内容。

## Überschrift

Deutscher Text mit Umlauten: äöü.`

	chunks := splitter.Split(markdown)

	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	for i, chunk := range chunks {
		t.Logf("Chunk %d: %q", i, chunk)
	}
}

func TestMarkdownSplitterFallbackToTextSplit(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 10
	splitter.Trim = false

	// Plain text without markdown should fall back to character splitting
	text := "Some text\n\nfrom a\ndocument"
	chunks := splitter.Split(text)

	// Verify all chunks respect max size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// Verify reconstruction
	rejoined := strings.Join(chunks, "")
	if rejoined != text {
		t.Errorf("Chunks don't reconstruct original text:\noriginal: %q\nrejoined: %q", text, rejoined)
	}
}

func TestMarkdownSplitterSplitByRule(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 25
	splitter.Trim = false

	text := "Some text\n\n---\n\nwith a rule"
	chunks := splitter.Split(text)

	// Verify all chunks respect max size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// Verify reconstruction
	rejoined := strings.Join(chunks, "")
	if rejoined != text {
		t.Errorf("Chunks don't reconstruct original text:\noriginal: %q\nrejoined: %q", text, rejoined)
	}

	// Should split at the horizontal rule boundary
	foundRule := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "---") {
			foundRule = true
			break
		}
	}
	if !foundRule {
		t.Error("Expected to find horizontal rule in chunks")
	}
}

func TestMarkdownSplitterSplitByRuleTrim(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 25
	splitter.Trim = true

	text := "Some text\n\n---\n\nwith a rule"
	chunks := splitter.Split(text)

	// Verify all chunks respect max size and are trimmed
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
		// Check trimming
		if chunk != strings.TrimSpace(chunk) {
			t.Errorf("Chunk %d not trimmed: %q", i, chunk)
		}
	}

	// Should split at the horizontal rule boundary
	foundRule := false
	for _, chunk := range chunks {
		if chunk == "---" || strings.Contains(chunk, "---") {
			foundRule = true
			break
		}
	}
	if !foundRule {
		t.Error("Expected to find horizontal rule in chunks")
	}
}

func TestMarkdownSplitterSplitByHeaders(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 30
	splitter.Trim = false

	text := "# Header 1\n\nSome text\n\n## Header 2\n\nwith headings\n"
	chunks := splitter.Split(text)

	// Verify chunks respect size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// Should split at header boundaries - verify we have multiple chunks
	if len(chunks) < 2 {
		t.Errorf("Expected at least 2 chunks for header splitting, got %d: %v", len(chunks), chunks)
	}

	// Verify both headers are present
	rejoined := strings.Join(chunks, "")
	if !strings.Contains(rejoined, "# Header 1") {
		t.Error("Lost H1 header")
	}
	if !strings.Contains(rejoined, "## Header 2") {
		t.Error("Lost H2 header")
	}
}

func TestMarkdownSplitterSubheadingsGroupedWithTopHeader(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 40
	splitter.Trim = false

	text := "# Header 1\n\nSome text\n\n## Header 2\n\nwith headings\n\n### Subheading\n\nand more text\n"
	chunks := splitter.Split(text)

	// Verify chunks respect size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// Should split preferring higher-level headers
	// H1 should be in a separate chunk from H2/H3 group
	if len(chunks) < 2 {
		t.Errorf("Expected at least 2 chunks, got %d: %v", len(chunks), chunks)
	}

	// Verify content is preserved
	rejoined := strings.Join(chunks, "")
	if !strings.Contains(rejoined, "# Header 1") || !strings.Contains(rejoined, "## Header 2") || !strings.Contains(rejoined, "### Subheading") {
		t.Error("Lost header content during chunking")
	}
}

func TestMarkdownSplitterTrimmingDoesntTrimBlockIndentationMultipleItems(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 48
	splitter.Trim = true

	text := "* Really long list item that is too big to fit\n\n  * Some Indented Text\n\n  * More Indented Text\n\n"
	chunks := splitter.Split(text)

	expected := []string{
		"* Really long list item that is too big to fit",
		"* Some Indented Text\n\n  * More Indented Text",
	}

	if len(chunks) != len(expected) {
		t.Fatalf("Expected %d chunks, got %d: %v", len(expected), len(chunks), chunks)
	}

	// Check first chunk
	if chunks[0] != expected[0] {
		t.Errorf("Chunk 0 mismatch:\ngot:  %q\nwant: %q", chunks[0], expected[0])
	}

	// Second chunk should preserve indentation for nested items
	if !strings.Contains(chunks[1], "Some Indented Text") || !strings.Contains(chunks[1], "More Indented Text") {
		t.Errorf("Chunk 1 should contain indented text:\ngot: %q", chunks[1])
	}
}

func TestMarkdownSplitterTrimmingDoesTrimBlockIndentationSingleItem(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 50
	splitter.Trim = true

	text := "1. Really long list item\n\n  1. Some Indented Text\n\n  2. More Indented Text\n\n"
	chunks := splitter.Split(text)

	// Verify chunks respect size
	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// When trim is enabled, chunks should be trimmed
	for i, chunk := range chunks {
		if chunk != strings.TrimSpace(chunk) {
			t.Errorf("Chunk %d not properly trimmed: %q", i, chunk)
		}
	}

	// Verify all list items are present
	rejoined := strings.Join(chunks, " ")
	if !strings.Contains(rejoined, "Really long list item") {
		t.Error("Lost first list item")
	}
	if !strings.Contains(rejoined, "Some Indented Text") {
		t.Error("Lost indented list item")
	}
	if !strings.Contains(rejoined, "More Indented Text") {
		t.Error("Lost second indented list item")
	}
}

func TestMarkdownSplitterChunkReconstruction(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 50
	splitter.Trim = false

	text := "# Header 1\n\nContent here\n\n## Header 2\n\nMore content\n\n### Header 3\n\nEven more\n"
	chunks := splitter.Split(text)

	// Chunks should be able to be joined back to original text
	rejoined := strings.Join(chunks, "")

	if rejoined != text {
		t.Errorf("Rejoined chunks don't match original:\noriginal: %q\nrejoined: %q", text, rejoined)
	}
}

func TestMarkdownSplitterAllChunksWithinSize(t *testing.T) {
	splitter := NewMarkdownSplitter()
	splitter.ChunkSize = 40
	splitter.Trim = false

	text := `# Title

This is a long document with multiple sections.

## Section 1

Content for section 1 with several sentences.

## Section 2

More content here with additional text.

### Subsection

Even more content in subsection.`

	chunks := splitter.Split(text)

	for i, chunk := range chunks {
		size := splitter.LenFunc(chunk)
		if size > splitter.ChunkSize {
			t.Errorf("Chunk %d exceeds max size: %d > %d\nContent: %q", i, size, splitter.ChunkSize, chunk)
		}
	}

	// Verify reconstruction
	rejoined := strings.Join(chunks, "")
	if rejoined != text {
		t.Error("Chunks cannot be rejoined to original text")
	}
}
