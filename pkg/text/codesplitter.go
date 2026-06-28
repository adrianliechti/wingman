package text

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// CodeSplitter splits source code into chunks at structural boundaries.
//
// Rather than parsing the code, it uses leading indentation and blank lines as a
// proxy for nesting depth: a line with less indentation (or one following a blank
// line) is a higher-priority split point than a deeply nested line. This keeps
// related lines together while staying language-agnostic and resilient to
// malformed input.
type CodeSplitter struct {
	SplitterOptions
}

// codeIndentCap bounds how many indentation levels are distinguished; deeper
// nesting collapses into the lowest-priority bucket.
const codeIndentCap = 8

// codeExtensions lists the file extensions treated as source code. For anything
// else NewCodeSplitter returns nil so the caller can fall back to prose splitting.
var codeExtensions = map[string]bool{
	".go": true,
	".py": true, ".pyi": true,
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true,
	".java": true, ".kt": true, ".kts": true, ".scala": true, ".groovy": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true,
	".cs": true, ".m": true, ".mm": true,
	".rs": true, ".swift": true, ".dart": true,
	".rb": true, ".php": true, ".pl": true, ".pm": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".lua": true, ".r": true, ".jl": true,
	".sql": true,
	".html": true, ".htm": true, ".xml": true,
	".css": true, ".scss": true, ".sass": true, ".less": true,
	".vue": true, ".svelte": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".proto": true, ".graphql": true, ".gql": true,
	".tf": true, ".hcl": true,
	".ex": true, ".exs": true, ".erl": true, ".hrl": true,
	".clj": true, ".cljs": true, ".cljc": true,
	".hs": true, ".ml": true, ".mli": true,
	".vim": true, ".el": true,
}

// NewCodeSplitter creates a CodeSplitter for the given filename, or nil if the
// extension is not recognised as source code.
func NewCodeSplitter(filename string) *CodeSplitter {
	ext := strings.ToLower(filepath.Ext(filename))

	if !codeExtensions[ext] {
		return nil
	}

	return &CodeSplitter{
		SplitterOptions: SplitterOptions{
			ChunkSize:    1500,
			ChunkOverlap: 0,
			Trim:         true,
			Normalize:    false,
			LenFunc:      utf8.RuneCountInString,
		},
	}
}

func (s *CodeSplitter) Split(text string) []string {
	if s.LenFunc(text) <= s.ChunkSize {
		if s.Trim {
			text = strings.TrimSpace(text)
		}
		if text == "" {
			return nil
		}
		return []string{text}
	}

	return s.mergeChunks(text, codeBoundaries(text))
}

// codeBoundaries returns the positions where a chunk may start, ranked by how
// good a split point each is. Every line start is a candidate; lines with less
// indentation (and lines after a blank line) rank higher so the merger prefers
// to break between top-level constructs rather than inside them.
func codeBoundaries(text string) []Boundary {
	var boundaries []Boundary

	n := len(text)
	lineStart := 0
	prevBlank := true

	for i := 0; i <= n; i++ {
		if i < n && text[i] != '\n' {
			continue
		}

		indent, blank := measureIndent(text[lineStart:i])

		if lineStart > 0 {
			level := LevelLineBreak

			if !blank {
				if indent > codeIndentCap {
					indent = codeIndentCap
				}

				level += SemanticLevel(codeIndentCap-indent) + 1

				if prevBlank {
					level++
				}
			}

			boundaries = append(boundaries, Boundary{
				Level: level,
				Start: lineStart,
				End:   lineStart,
			})
		}

		prevBlank = blank
		lineStart = i + 1
	}

	return boundaries
}

// measureIndent returns the leading-whitespace width of a line (each space or tab
// counts as one) and whether the line is blank.
func measureIndent(line string) (indent int, blank bool) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ', '\t':
			indent++
		case '\r':
		default:
			return indent, false
		}
	}

	return 0, true
}

// mergeChunks greedily builds chunks by finding the farthest boundary that keeps
// the chunk within size limits, preferring higher semantic levels.
func (s *CodeSplitter) mergeChunks(text string, boundaries []Boundary) []string {
	var result []string
	cursor := 0
	textLen := len(text)

	for cursor < textLen {
		// Find all boundary positions after cursor
		remaining := text[cursor:]
		if s.LenFunc(remaining) <= s.ChunkSize {
			// Everything left fits in one chunk
			if s.Trim {
				remaining = strings.TrimSpace(remaining)
			}
			if remaining != "" {
				result = append(result, remaining)
			}
			break
		}

		// Find the best split point: the farthest boundary that keeps chunk within size
		bestEnd := -1

		// Determine the max semantic level available
		maxLevel := SemanticLevel(0)
		for _, b := range boundaries {
			if b.Start > cursor && b.Level > maxLevel {
				maxLevel = b.Level
			}
		}

		// Try from highest semantic level down
		for level := maxLevel; level >= LevelLineBreak; level-- {
			// Collect boundary positions at this level or higher
			var positions []int
			for _, b := range boundaries {
				if b.Start > cursor && b.Level >= level {
					positions = append(positions, b.Start)
				}
			}

			if len(positions) == 0 {
				continue
			}

			// Binary search for the farthest position that fits
			low, high := 0, len(positions)-1
			found := -1
			for low <= high {
				mid := (low + high) / 2
				chunk := text[cursor:positions[mid]]
				if s.LenFunc(chunk) <= s.ChunkSize {
					found = positions[mid]
					low = mid + 1
				} else {
					high = mid - 1
				}
			}

			if found > cursor {
				bestEnd = found
				break
			}
		}

		if bestEnd <= cursor {
			// No boundary fits; split at character limit
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
			bestEnd = end
		}

		chunk := text[cursor:bestEnd]
		if s.Trim {
			chunk = strings.TrimSpace(chunk)
		}
		if chunk != "" {
			result = append(result, chunk)
		}

		// Handle overlap: find the earliest boundary whose distance to bestEnd fits in ChunkOverlap
		if s.ChunkOverlap > 0 && bestEnd < textLen {
			overlapStart := bestEnd
			for _, b := range boundaries {
				if b.Start <= cursor || b.Start >= bestEnd {
					continue
				}
				if s.LenFunc(text[b.Start:bestEnd]) <= s.ChunkOverlap {
					overlapStart = b.Start
					break
				}
			}
			if overlapStart > cursor && overlapStart < bestEnd {
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
