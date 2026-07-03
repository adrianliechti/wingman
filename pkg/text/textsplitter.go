package text

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SemanticLevel represents different levels of text segmentation.
type SemanticLevel int

const (
	LevelWord SemanticLevel = iota
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
			// Count consecutive line endings (\r\n counts as one)
			start := i
			for i+1 < n && (text[i+1] == '\n' || text[i+1] == '\r') {
				i++
			}
			end := i + 1
			count := 0
			for j := start; j < end; j++ {
				if text[j] == '\r' && j+1 < end && text[j+1] == '\n' {
					count++ // \r\n = one line ending
					j++     // skip the \n
				} else {
					count++ // standalone \r or \n
				}
			}

			level := LevelWord // single newline = word-level break
			if count >= 2 {
				level = LevelLineBreak // paragraph break
			}

			positions = append(positions, splitPosition{offset: end, level: level})
		} else if (ch == '.' || ch == '!' || ch == '?') && i+1 < n && unicode.IsSpace(rune(text[i+1])) {
			// Sentence boundary: skip trailing whitespace (\r\n counts as one line ending)
			end := i + 1
			newlines := 0
			for end < n && unicode.IsSpace(rune(text[end])) {
				if text[end] == '\n' || (text[end] == '\r' && (end+1 >= n || text[end+1] != '\n')) {
					newlines++
				}
				end++
			}

			level := LevelSentence
			if newlines >= 2 {
				level = LevelLineBreak // paragraph break
			}

			positions = append(positions, splitPosition{offset: end, level: level})
			i = end - 1
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
// Prefix lengths are precomputed once so size checks are O(1) instead of
// re-measuring text from the cursor for every candidate position.
func (s *TextSplitter) buildChunks(text string, positions []splitPosition) []string {
	prefix := make([]int, len(positions))
	last, lastLen := 0, 0
	for i, p := range positions {
		lastLen += s.LenFunc(text[last:p.offset])
		prefix[i] = lastLen
		last = p.offset
	}
	totalLen := lastLen + s.LenFunc(text[last:])

	var result []string
	cursor, cursorLen := 0, 0
	textLen := len(text)

	for cursor < textLen {
		if totalLen-cursorLen <= s.ChunkSize {
			remaining := text[cursor:]
			if s.Trim {
				remaining = strings.TrimSpace(remaining)
			}
			if remaining != "" {
				result = append(result, remaining)
			}
			break
		}

		bestEnd, bestLen := s.findBestSplit(text, positions, prefix, cursor, cursorLen)

		chunk := text[cursor:bestEnd]
		if s.Trim {
			chunk = strings.TrimSpace(chunk)
		}
		if chunk != "" {
			result = append(result, chunk)
		}

		// Handle overlap
		if s.ChunkOverlap > 0 && bestEnd < textLen {
			if idx := s.findOverlapStart(positions, prefix, cursor, bestEnd, bestLen); idx >= 0 {
				cursor, cursorLen = positions[idx].offset, prefix[idx]
				continue
			}
		}
		cursor, cursorLen = bestEnd, bestLen
	}

	return result
}

// findBestSplit finds the farthest split point from cursor that keeps the chunk within size.
// Tries higher semantic levels first for better split quality.
// Returns the split offset and the prefix length at that offset.
func (s *TextSplitter) findBestSplit(text string, positions []splitPosition, prefix []int, cursor, cursorLen int) (int, int) {
	// The window of positions after cursor whose chunk fits within size
	startIdx := sort.Search(len(positions), func(i int) bool {
		return positions[i].offset > cursor
	})
	endIdx := sort.Search(len(positions), func(i int) bool {
		return prefix[i]-cursorLen > s.ChunkSize
	})

	// Try each level from highest to lowest, farthest position first
	for level := LevelLineBreak; level >= LevelWord; level-- {
		for i := endIdx - 1; i >= startIdx; i-- {
			if positions[i].level >= level {
				return positions[i].offset, prefix[i]
			}
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
		size = s.LenFunc(text[cursor:end])
	}
	return end, cursorLen + size
}

// findOverlapStart finds where to start the next chunk for overlap.
// It returns the index of the earliest split position whose distance to the chunk
// end is within ChunkOverlap (maximizing the overlap), or -1 if there is none.
func (s *TextSplitter) findOverlapStart(positions []splitPosition, prefix []int, cursor, chunkEnd, chunkEndLen int) int {
	startIdx := sort.Search(len(positions), func(i int) bool {
		return positions[i].offset > cursor
	})

	idx := startIdx + sort.Search(len(positions)-startIdx, func(i int) bool {
		return chunkEndLen-prefix[startIdx+i] <= s.ChunkOverlap
	})

	if idx < len(positions) && positions[idx].offset < chunkEnd {
		return idx
	}
	return -1
}
