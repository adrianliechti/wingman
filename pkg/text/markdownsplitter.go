package text

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// MarkdownElement represents different semantic levels in Markdown
type MarkdownElement int

const (
	// Ordered from lowest to highest priority for splitting
	ElementInline MarkdownElement = iota
	ElementSoftBreak
	ElementBlock
	ElementRule
	ElementHeading6
	ElementHeading5
	ElementHeading4
	ElementHeading3
	ElementHeading2
	ElementHeading1
)

type MarkdownSplitter struct {
	SplitterOptions
}

func NewMarkdownSplitter() MarkdownSplitter {
	return MarkdownSplitter{
		SplitterOptions: SplitterOptions{
			ChunkSize:    1500,
			ChunkOverlap: 0,

			Trim:      true,
			Normalize: false,

			LenFunc: utf8.RuneCountInString,
		},
	}
}

func (s *MarkdownSplitter) Split(text string) []string {
	// Parse markdown and extract boundaries
	boundaries := s.parseMarkdownBoundaries(text)

	if len(boundaries) == 0 {
		// No markdown boundaries found, return single chunk or split by characters
		if s.LenFunc(text) <= s.ChunkSize {
			if s.Trim {
				text = strings.TrimSpace(text)
			}
			if text != "" {
				return []string{text}
			}
			return []string{}
		}
		return s.splitByCharacters(text)
	}

	return s.buildChunks(text, boundaries)
}

// parseMarkdownBoundaries extracts semantic boundaries from markdown AST
func (s *MarkdownSplitter) parseMarkdownBoundaries(markdown string) []Boundary {
	var boundaries []Boundary

	source := []byte(markdown)
	reader := text.NewReader(source)
	doc := parser.NewParser().Parse(reader)

	// Walk the AST and extract boundaries
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		// Skip nodes without line segments
		if n.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}

		segment := n.Lines().At(0)
		start := segment.Start
		stop := segment.Stop

		// Get full range for nodes with multiple lines
		if n.Lines().Len() > 1 {
			lastSegment := n.Lines().At(n.Lines().Len() - 1)
			stop = lastSegment.Stop
		}

		var element MarkdownElement
		var shouldAdd bool

		switch n.Kind() {
		// Headings - highest priority, ordered by level
		case ast.KindHeading:
			heading := n.(*ast.Heading)
			switch heading.Level {
			case 1:
				element = ElementHeading1
			case 2:
				element = ElementHeading2
			case 3:
				element = ElementHeading3
			case 4:
				element = ElementHeading4
			case 5:
				element = ElementHeading5
			case 6:
				element = ElementHeading6
			}
			shouldAdd = true

		// Thematic break / horizontal rule
		case ast.KindThematicBreak:
			element = ElementRule
			shouldAdd = true

		// Block elements
		case ast.KindParagraph,
			ast.KindCodeBlock,
			ast.KindFencedCodeBlock,
			ast.KindHTMLBlock,
			ast.KindBlockquote,
			ast.KindList,
			ast.KindListItem:
			element = ElementBlock
			shouldAdd = true

		// Inline elements
		case ast.KindText,
			ast.KindEmphasis,
			ast.KindCodeSpan,
			ast.KindLink,
			ast.KindImage,
			ast.KindAutoLink,
			ast.KindRawHTML:
			element = ElementInline
			shouldAdd = true
		}

		if shouldAdd {
			boundaries = append(boundaries, Boundary{
				Level: SemanticLevel(element),
				Start: start,
				End:   stop,
			})
		}

		return ast.WalkContinue, nil
	})

	// Sort boundaries by position, then by level (higher level first for same position)
	sort.Slice(boundaries, func(i, j int) bool {
		if boundaries[i].Start != boundaries[j].Start {
			return boundaries[i].Start < boundaries[j].Start
		}
		return boundaries[i].Level > boundaries[j].Level
	})

	return boundaries
}

// buildChunks constructs chunks using semantic boundaries
// This is adapted from TextSplitter but optimized for Markdown heading hierarchy
func (s *MarkdownSplitter) buildChunks(text string, boundaries []Boundary) []string {
	var result []string
	cursor := 0
	lastCursor := -1

	for cursor < len(text) {
		// Safety check to prevent infinite loops
		if cursor == lastCursor {
			cursor++
			if cursor >= len(text) {
				break
			}
		}
		lastCursor = cursor

		chunk, nextCursor := s.findOptimalChunk(text, boundaries, cursor)

		if chunk == "" {
			break
		}

		if s.Trim {
			chunk = strings.TrimSpace(chunk)
		}

		if chunk != "" {
			result = append(result, chunk)
		}

		// Calculate overlap position if needed
		if s.ChunkOverlap > 0 && nextCursor < len(text) {
			overlapStart := s.calculateOverlapStart(text, boundaries, cursor, nextCursor)
			// Ensure we always make progress
			if overlapStart <= cursor {
				cursor = nextCursor
			} else {
				cursor = overlapStart
			}
		} else {
			cursor = nextCursor
		}
	}

	return result
}

// findOptimalChunk uses binary search to find the largest chunk that fits
// Prioritizes breaking at higher-level headings for better semantic grouping
func (s *MarkdownSplitter) findOptimalChunk(text string, boundaries []Boundary, start int) (string, int) {
	if start >= len(text) {
		return "", len(text)
	}

	// Get boundaries after current position
	var validBoundaries []Boundary
	for _, b := range boundaries {
		if b.Start >= start {
			validBoundaries = append(validBoundaries, b)
		}
	}

	// If no boundaries, check if remaining text fits, otherwise split by characters
	if len(validBoundaries) == 0 {
		remaining := text[start:]
		if s.LenFunc(remaining) <= s.ChunkSize {
			return remaining, len(text)
		}
		return s.splitByCharactersFrom(text, start)
	}

	// Try each semantic level from highest to lowest
	// For markdown, this means H1 > H2 > H3 > ... > Rule > Block > SoftBreak > Inline
	for level := SemanticLevel(ElementHeading1); level >= SemanticLevel(ElementInline); level-- {
		chunk, end, found := s.findChunkAtLevel(text, validBoundaries, start, level)
		if found {
			return chunk, end
		}
	}

	// If we couldn't find anything at semantic boundaries, check if all fits
	remaining := text[start:]
	if s.LenFunc(remaining) <= s.ChunkSize {
		return remaining, len(text)
	}

	// Fallback: take at least one character
	end := start + 1
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	return text[start:end], end
}

// findChunkAtLevel attempts to find a chunk at a specific semantic level
func (s *MarkdownSplitter) findChunkAtLevel(text string, boundaries []Boundary, start int, level SemanticLevel) (string, int, bool) {
	// Filter boundaries at this level or higher
	var levelBoundaries []int
	for _, b := range boundaries {
		if b.Level >= level && b.Start >= start {
			levelBoundaries = append(levelBoundaries, b.End)
		}
	}

	if len(levelBoundaries) == 0 {
		return "", 0, false
	}

	// Add end of text as a potential boundary
	levelBoundaries = append(levelBoundaries, len(text))

	// Binary search for the largest chunk that fits
	low := 0
	high := len(levelBoundaries) - 1
	bestEnd := -1

	for low <= high {
		mid := (low + high) / 2
		end := levelBoundaries[mid]
		chunk := text[start:end]
		size := s.LenFunc(chunk)

		if size <= s.ChunkSize {
			bestEnd = end
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	if bestEnd > start {
		return text[start:bestEnd], bestEnd, true
	}

	return "", 0, false
}

// calculateOverlapStart finds where to start the next chunk for overlap
func (s *MarkdownSplitter) calculateOverlapStart(text string, boundaries []Boundary, chunkStart, chunkEnd int) int {
	if s.ChunkOverlap <= 0 || chunkEnd >= len(text) {
		return chunkEnd
	}

	targetSize := s.ChunkOverlap

	// Find boundaries within the chunk
	var chunkBoundaries []int
	for _, b := range boundaries {
		if b.Start > chunkStart && b.End <= chunkEnd {
			chunkBoundaries = append(chunkBoundaries, b.Start)
		}
	}

	if len(chunkBoundaries) == 0 {
		// No semantic boundaries, use character-based overlap
		overlapStart := chunkEnd - targetSize
		if overlapStart < chunkStart {
			overlapStart = chunkStart
		}
		// Adjust to UTF-8 boundary
		for overlapStart > chunkStart && !utf8.RuneStart(text[overlapStart]) {
			overlapStart--
		}
		return overlapStart
	}

	// Binary search for best boundary for overlap
	sort.Ints(chunkBoundaries)
	low := 0
	high := len(chunkBoundaries) - 1
	bestStart := chunkEnd

	for low <= high {
		mid := (low + high) / 2
		pos := chunkBoundaries[mid]
		overlapText := text[pos:chunkEnd]
		size := s.LenFunc(overlapText)

		if size <= targetSize {
			bestStart = pos
			high = mid - 1
		} else {
			low = mid + 1
		}
	}

	return bestStart
}

// splitByCharacters splits text by individual characters when no boundaries work
func (s *MarkdownSplitter) splitByCharacters(text string) []string {
	var result []string
	var current strings.Builder
	currentSize := 0

	for _, r := range text {
		runeSize := 1
		current.WriteRune(r)
		currentSize += runeSize

		if currentSize >= s.ChunkSize {
			chunk := current.String()
			if s.Trim {
				chunk = strings.TrimSpace(chunk)
			}
			if chunk != "" {
				result = append(result, chunk)
			}
			current.Reset()
			currentSize = 0
		}
	}

	if current.Len() > 0 {
		chunk := current.String()
		if s.Trim {
			chunk = strings.TrimSpace(chunk)
		}
		if chunk != "" {
			result = append(result, chunk)
		}
	}

	return result
}

// splitByCharactersFrom splits from a specific position
func (s *MarkdownSplitter) splitByCharactersFrom(text string, start int) (string, int) {
	remaining := text[start:]
	size := 0
	end := start

	for i, r := range remaining {
		if size >= s.ChunkSize {
			break
		}
		size++
		end = start + i + utf8.RuneLen(r)
	}

	if end <= start {
		end = start + 1
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
	}

	return text[start:end], end
}
