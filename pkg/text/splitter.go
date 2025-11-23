package text

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SemanticLevel represents different levels of text segmentation
type SemanticLevel int

const (
	LevelChar SemanticLevel = iota
	LevelWord
	LevelSentence
	LevelLineBreak
)

// Boundary represents a semantic boundary in the text
type Boundary struct {
	Level SemanticLevel
	Start int
	End   int
}

type Splitter struct {
	ChunkSize    int
	ChunkOverlap int

	Separators []string

	LenFunc   func(string) int
	Trim      bool
	Normalize bool
}

func NewSplitter() Splitter {
	s := Splitter{
		ChunkSize:    1500,
		ChunkOverlap: 0,

		Separators: []string{
			"\n\n",
			"\n",
			" ",
			"",
		},

		LenFunc:   utf8.RuneCountInString,
		Trim:      true,
		Normalize: false,
	}

	return s
}

func (s *Splitter) Split(text string) []string {
	if s.Normalize {
		text = Normalize(text)
	}

	// Parse all semantic boundaries in the text
	boundaries := s.parseBoundaries(text)

	if len(boundaries) == 0 {
		// No boundaries found, return single chunk or split by characters
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

// parseBoundaries identifies all semantic boundaries in the text
func (s *Splitter) parseBoundaries(text string) []Boundary {
	var boundaries []Boundary

	// Parse line breaks (highest semantic level)
	boundaries = append(boundaries, s.parseLineBreaks(text)...)

	// Parse sentence boundaries
	boundaries = append(boundaries, s.parseSentences(text)...)

	// Parse word boundaries
	boundaries = append(boundaries, s.parseWords(text)...)

	// Sort boundaries by position, then by level (higher level first for same position)
	sort.Slice(boundaries, func(i, j int) bool {
		if boundaries[i].Start != boundaries[j].Start {
			return boundaries[i].Start < boundaries[j].Start
		}
		return boundaries[i].Level > boundaries[j].Level
	})

	return boundaries
}

// parseLineBreaks finds line break sequences (multiple newlines)
func (s *Splitter) parseLineBreaks(text string) []Boundary {
	var boundaries []Boundary
	inBreak := false
	start := 0

	for i := 0; i < len(text); i++ {
		isNewline := text[i] == '\n' || text[i] == '\r'

		if isNewline && !inBreak {
			start = i
			inBreak = true
		} else if !isNewline && inBreak {
			// Count newlines in sequence
			sequence := text[start:i]
			newlineCount := 0
			for _, ch := range sequence {
				if ch == '\n' || ch == '\r' {
					newlineCount++
				}
			}

			// Only consider multiple newlines as line breaks
			if newlineCount >= 2 {
				boundaries = append(boundaries, Boundary{
					Level: LevelLineBreak,
					Start: start,
					End:   i,
				})
			}
			inBreak = false
		}
	}

	// Handle trailing line break
	if inBreak {
		sequence := text[start:]
		newlineCount := 0
		for _, ch := range sequence {
			if ch == '\n' || ch == '\r' {
				newlineCount++
			}
		}
		if newlineCount >= 2 {
			boundaries = append(boundaries, Boundary{
				Level: LevelLineBreak,
				Start: start,
				End:   len(text),
			})
		}
	}

	return boundaries
}

// parseSentences finds sentence boundaries (. ! ? followed by space/newline)
func (s *Splitter) parseSentences(text string) []Boundary {
	var boundaries []Boundary

	for i := 0; i < len(text)-1; i++ {
		ch := text[i]
		next := text[i+1]

		// Sentence ends with . ! ? followed by space or newline
		if (ch == '.' || ch == '!' || ch == '?') && (next == ' ' || next == '\n' || next == '\r' || next == '\t') {
			// Include the punctuation and following whitespace
			end := i + 2
			for end < len(text) && unicode.IsSpace(rune(text[end])) {
				end++
			}

			boundaries = append(boundaries, Boundary{
				Level: LevelSentence,
				Start: i + 1,
				End:   end,
			})
		}
	}

	return boundaries
}

// parseWords finds word boundaries (spaces, tabs)
func (s *Splitter) parseWords(text string) []Boundary {
	var boundaries []Boundary

	for i := 0; i < len(text); i++ {
		if unicode.IsSpace(rune(text[i])) {
			start := i
			end := i + 1

			// Extend to include consecutive whitespace
			for end < len(text) && unicode.IsSpace(rune(text[end])) {
				end++
			}

			boundaries = append(boundaries, Boundary{
				Level: LevelWord,
				Start: start,
				End:   end,
			})

			i = end - 1
		}
	}

	return boundaries
}

// buildChunks constructs chunks using semantic boundaries
func (s *Splitter) buildChunks(text string, boundaries []Boundary) []string {
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
func (s *Splitter) findOptimalChunk(text string, boundaries []Boundary, start int) (string, int) {
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
	for level := LevelLineBreak; level >= LevelChar; level-- {
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
func (s *Splitter) findChunkAtLevel(text string, boundaries []Boundary, start int, level SemanticLevel) (string, int, bool) {
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
func (s *Splitter) calculateOverlapStart(text string, boundaries []Boundary, chunkStart, chunkEnd int) int {
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
func (s *Splitter) splitByCharacters(text string) []string {
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
func (s *Splitter) splitByCharactersFrom(text string, start int) (string, int) {
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

func (s *Splitter) textSeparator(text string) string {
	for _, sep := range s.Separators {
		if strings.Contains(text, sep) {
			return sep
		}
	}

	if len(s.Separators) > 0 {
		return s.Separators[len(s.Separators)-1]
	}

	return ""
}
