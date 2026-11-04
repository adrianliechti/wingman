package scrape

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// focusedText ranks overlapping windows locally. Output is original text, never
// generated evidence. Offsets always refer to the complete source document.
func focusedText(text, query string, budget int) string {
	chars := []rune(text)
	if len(chars) <= budget {
		return text
	}
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	slices.Sort(terms)
	terms = slices.Compact(terms)
	terms = slices.DeleteFunc(terms, func(term string) bool { return utf8.RuneCountInString(term) < 2 })
	if len(terms) == 0 || budget < 256 {
		return paginate(text, 0, budget)
	}
	type passage struct {
		start, end int
		matches    []string
		score      float64
	}
	width := min(1400, budget-200)
	stride := width - min(400, width/3)
	var passages []passage
	frequency := map[string]int{}
	phrase := strings.ToLower(strings.TrimSpace(query))
	for start := 0; start < len(chars); start += stride {
		end := min(start+width, len(chars))
		lower := strings.ToLower(string(chars[start:end]))
		p := passage{start: start, end: end}
		for _, term := range terms {
			if strings.Contains(lower, term) {
				p.matches = append(p.matches, term)
				frequency[term]++
			}
		}
		if strings.Contains(lower, phrase) {
			p.score += 2
		}
		for line := range strings.SplitSeq(lower, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				for _, term := range p.matches {
					if strings.Contains(line, term) {
						p.score += 2
					}
				}
			}
		}
		passages = append(passages, p)
	}
	for i := range passages {
		for _, term := range passages[i].matches {
			passages[i].score += math.Log(1 + float64(len(passages))/float64(frequency[term]))
		}
	}
	slices.SortStableFunc(passages, func(a, b passage) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})
	if passages[0].score == 0 {
		return "[No keyword matches; showing the beginning, not evidence of absence.]\n" + paginate(text, 0, budget)
	}
	notice := "[Selected excerpts; omitted text may matter. Use start_index without query to read surrounding text.]\n"
	remaining := budget - utf8.RuneCountInString(notice)
	var selected []passage
	for _, p := range passages {
		if p.score == 0 {
			break
		}
		overlaps := false
		for _, previous := range selected {
			overlaps = overlaps || (p.start < previous.end && previous.start < p.end)
		}
		if overlaps {
			continue
		}
		cost := p.end - p.start + utf8.RuneCountInString(fmt.Sprintf("\n[Characters %d-%d of %d]\n", p.start, p.end, len(chars)))
		if cost > remaining {
			continue
		}
		selected = append(selected, p)
		remaining -= cost
	}
	slices.SortFunc(selected, func(a, b passage) int { return a.start - b.start })
	var out strings.Builder
	out.WriteString(notice)
	for _, p := range selected {
		fmt.Fprintf(&out, "\n[Characters %d-%d of %d]\n", p.start, p.end, len(chars))
		out.WriteString(string(chars[p.start:p.end]))
	}
	return out.String()
}
