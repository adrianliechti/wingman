package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SemanticLevel represents different levels of text segmentation.
type SemanticLevel int

const (
	LevelChar SemanticLevel = iota
	LevelWord
	LevelSentence
	LevelLineBreak
)

// Boundary represents a semantic boundary in the text.
type Boundary struct {
	Level SemanticLevel
	Start int
	End   int
}

// TextSplitter splits plain text at semantic boundaries
// (paragraph breaks > sentences > words > characters).
type TextSplitter struct {
	SplitterOptions
}

func NewTextSplitter() TextSplitter {
	return TextSplitter{
		SplitterOptions: SplitterOptions{
			ChunkSize:    1500,
			ChunkOverlap: 0,
			Trim:         true,
			Normalize:    false,
			LenFunc:      utf8.RuneCountInString,
		},
	}
}

func (s *TextSplitter) Split(input string) []string {
	if s.Normalize {
		input = Normalize(input)
	}

	if s.LenFunc(input) <= s.ChunkSize {
		if s.Trim {
			input = strings.TrimSpace(input)
		}
		if input == "" {
			return nil
		}
		return []string{input}
	}

	// Collect all split positions with their semantic level
	positions := s.findSplitPositions(input)

	return s.buildChunks(input, positions)
}

// splitPosition is a byte offset where we can split, with a semantic level.
type splitPosition struct {
	offset int
	level  SemanticLevel
}

// findSplitPositions scans the text once and collects all potential split points.
func (s *TextSplitter) findSplitPositions(text string) []splitPosition {
	var positions []splitPosition
	n := len(text)

	for i := 0; i < n; i++ {
		ch := text[i]

		if ch == '\n' || ch == '\r' {
			// Count consecutive newlines
			start := i
			for i+1 < n && (text[i+1] == '\n' || text[i+1] == '\r') {
				i++
			}
			end := i + 1
			count := 0
			for j := start; j < end; j++ {
				if text[j] == '\n' {
					count++
				}
			}

			level := LevelWord // single newline = word-level break
			if count >= 2 {
				level = LevelLineBreak // paragraph break
			}

			positions = append(positions, splitPosition{offset: end, level: level})
		} else if (ch == '.' || ch == '!' || ch == '?') && i+1 < n && unicode.IsSpace(rune(text[i+1])) {
			// Sentence boundary: skip trailing whitespace
			end := i + 1
			for end < n && unicode.IsSpace(rune(text[end])) {
				end++
			}
			positions = append(positions, splitPosition{offset: end, level: LevelSentence})
		} else if ch == ' ' || ch == '\t' {
			// Word boundary
			end := i + 1
			for end < n && (text[end] == ' ' || text[end] == '\t') {
				end++
			}
			positions = append(positions, splitPosition{offset: end, level: LevelWord})
			i = end - 1
		}
	}

	return positions
}

// buildChunks greedily fills chunks by finding the farthest split point that fits.
func (s *TextSplitter) buildChunks(text string, positions []splitPosition) []string {
	var result []string
	cursor := 0
	textLen := len(text)

	for cursor < textLen {
		remaining := text[cursor:]
		if s.LenFunc(remaining) <= s.ChunkSize {
			if s.Trim {
				remaining = strings.TrimSpace(remaining)
			}
			if remaining != "" {
				result = append(result, remaining)
			}
			break
		}

		bestEnd := s.findBestSplit(text, positions, cursor)

		chunk := text[cursor:bestEnd]
		if s.Trim {
			chunk = strings.TrimSpace(chunk)
		}
		if chunk != "" {
			result = append(result, chunk)
		}

		// Handle overlap
		if s.ChunkOverlap > 0 && bestEnd < textLen {
			overlapStart := s.findOverlapStart(text, positions, cursor, bestEnd)
			if overlapStart > cursor {
				cursor = overlapStart
			} else {
				cursor = bestEnd
			}
		} else {
			cursor = bestEnd
		}
	}

	return result
}

// findBestSplit finds the farthest split point from cursor that keeps the chunk within size.
// Tries higher semantic levels first for better split quality.
func (s *TextSplitter) findBestSplit(text string, positions []splitPosition, cursor int) int {
	// Try each level from highest to lowest
	for level := LevelLineBreak; level >= LevelWord; level-- {
		best := -1
		for _, p := range positions {
			if p.offset <= cursor || p.level < level {
				continue
			}
			if s.LenFunc(text[cursor:p.offset]) <= s.ChunkSize {
				best = p.offset
			} else {
				break // positions are ordered, no point continuing
			}
		}
		if best > cursor {
			return best
		}
	}

	// Character fallback
	end := cursor
	size := 0
	for i, r := range text[cursor:] {
		if size >= s.ChunkSize {
			break
		}
		size++
		end = cursor + i + utf8.RuneLen(r)
	}
	if end <= cursor {
		end = cursor + 1
	}
	return end
}

// findOverlapStart finds where to start the next chunk for overlap.
// It finds the earliest split position whose distance to chunkEnd is within ChunkOverlap,
// maximizing the overlap while respecting the limit.
func (s *TextSplitter) findOverlapStart(text string, positions []splitPosition, cursor, chunkEnd int) int {
	best := chunkEnd
	for _, p := range positions {
		if p.offset <= cursor || p.offset >= chunkEnd {
			continue
		}
		overlapSize := s.LenFunc(text[p.offset:chunkEnd])
		if overlapSize <= s.ChunkOverlap {
			// This is the earliest (farthest back) position that fits — gives max overlap
			best = p.offset
			break
		}
	}
	return best
}
