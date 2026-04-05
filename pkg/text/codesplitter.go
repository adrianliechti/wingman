package text

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// CodeSplitter splits code using tree-sitter AST for syntax-aware chunking.
// It uses AST node depth as semantic level: shallower nodes (top-level declarations)
// are higher-priority split points than deeper nodes (statements within a function).
type CodeSplitter struct {
	SplitterOptions

	// Language is the tree-sitter language to use for parsing.
	// If nil, the splitter falls back to TextSplitter behavior.
	Language *gotreesitter.Language
}

// NewCodeSplitter creates a CodeSplitter for the given filename.
// It auto-detects the language from the file extension using tree-sitter's registry.
// Returns nil if the language is not supported.
func NewCodeSplitter(filename string) *CodeSplitter {
	entry := grammars.DetectLanguage(filename)
	if entry == nil {
		return nil
	}

	lang := entry.Language()
	if lang == nil {
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
		Language: lang,
	}
}

// codeSection represents a section of code identified by tree-sitter AST traversal.
type codeSection struct {
	start int // byte offset
	end   int // byte offset
	depth int // AST depth (0 = top-level)
}

func (s *CodeSplitter) Split(text string) []string {
	if s.Language == nil {
		// Fallback to text splitter
		ts := NewTextSplitter()
		ts.SplitterOptions = s.SplitterOptions
		return ts.Split(text)
	}

	source := []byte(text)

	parser := gotreesitter.NewParser(s.Language)

	tree, err := parser.Parse(source)
	if err != nil || tree == nil {
		// Parse failed, fall back to text splitter
		ts := NewTextSplitter()
		ts.SplitterOptions = s.SplitterOptions
		return ts.Split(text)
	}
	defer tree.Release()

	root := tree.RootNode()
	if root == nil {
		ts := NewTextSplitter()
		ts.SplitterOptions = s.SplitterOptions
		return ts.Split(text)
	}

	// Collect top-level and second-level named nodes as split boundaries
	sections := s.collectSections(root)

	if len(sections) == 0 {
		if s.LenFunc(text) <= s.ChunkSize {
			if s.Trim {
				text = strings.TrimSpace(text)
			}
			if text != "" {
				return []string{text}
			}
			return []string{}
		}
		ts := NewTextSplitter()
		ts.SplitterOptions = s.SplitterOptions
		return ts.Split(text)
	}

	return s.buildChunksFromSections(text, sections)
}

// collectSections walks the AST and collects named nodes with their depth.
// Only collects nodes up to a reasonable depth for splitting purposes.
func (s *CodeSplitter) collectSections(root *gotreesitter.Node) []codeSection {
	var sections []codeSection

	// Walk the tree iteratively using a stack
	type stackItem struct {
		node  *gotreesitter.Node
		depth int
	}

	maxDepth := 4 // Don't go too deep; we only need meaningful structural boundaries
	stack := []stackItem{{node: root, depth: 0}}

	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		node := item.node
		depth := item.depth

		// Only collect named nodes (skip anonymous tokens like punctuation)
		if depth > 0 && node.IsNamed() {
			start := int(node.StartByte())
			end := int(node.EndByte())

			if start < end {
				sections = append(sections, codeSection{
					start: start,
					end:   end,
					depth: depth,
				})
			}
		}

		// Push children in reverse order so we process them left-to-right
		if depth < maxDepth {
			for i := node.ChildCount() - 1; i >= 0; i-- {
				child := node.Child(i)
				if child != nil {
					stack = append(stack, stackItem{node: child, depth: depth + 1})
				}
			}
		}
	}

	// Sort by start position
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].start < sections[j].start
	})

	return sections
}

// buildChunksFromSections builds chunks by merging adjacent sections that fit
// within the chunk size, preferring to split at shallower (higher-priority) boundaries.
func (s *CodeSplitter) buildChunksFromSections(text string, sections []codeSection) []string {
	// Convert sections to boundaries: positions where we can split, with semantic levels.
	// Lower depth = higher semantic level (better split point).
	// We invert depth so that depth 1 (top-level declarations) gets the highest level.
	maxDepth := 0
	for _, sec := range sections {
		if sec.depth > maxDepth {
			maxDepth = sec.depth
		}
	}

	var boundaries []Boundary
	seen := make(map[int]bool)

	for _, sec := range sections {
		if seen[sec.start] {
			continue
		}
		seen[sec.start] = true

		// Invert: depth 1 -> highest level, depth N -> lowest level
		level := SemanticLevel(maxDepth - sec.depth + int(LevelLineBreak) + 1)

		boundaries = append(boundaries, Boundary{
			Level: level,
			Start: sec.start,
			End:   sec.start, // boundary point is the start of the node
		})
	}

	// Also add line-break boundaries for finer-grained splitting within large nodes
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			if !seen[i+1] {
				boundaries = append(boundaries, Boundary{
					Level: LevelLineBreak,
					Start: i + 1,
					End:   i + 1,
				})
			}
		}
	}

	sort.Slice(boundaries, func(i, j int) bool {
		if boundaries[i].Start != boundaries[j].Start {
			return boundaries[i].Start < boundaries[j].Start
		}
		return boundaries[i].Level > boundaries[j].Level
	})

	return s.mergeChunks(text, boundaries)
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
