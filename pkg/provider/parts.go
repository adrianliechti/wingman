package provider

import (
	"sort"
	"strings"
)

// PartAccumulator owns indexed part deltas. Both streaming and complete
// responses use the same merge rules, including sparse and interleaved indexes.
// Snapshots do not alias mutable accumulator storage.
type PartAccumulator struct {
	parts []*accumulatedPart
}

type accumulatedPart struct {
	index int
	text  strings.Builder
	file  *File
}

func (a *PartAccumulator) Add(delta Part) {
	i := sort.Search(len(a.parts), func(i int) bool { return a.parts[i].index >= delta.Index })
	if i == len(a.parts) || a.parts[i].index != delta.Index {
		a.parts = append(a.parts, nil)
		copy(a.parts[i+1:], a.parts[i:])
		a.parts[i] = &accumulatedPart{index: delta.Index}
	}
	p := a.parts[i]
	p.text.WriteString(delta.Text)
	if delta.File != nil {
		p.file = delta.File
	}
}

func (a *PartAccumulator) Len() int { return len(a.parts) }

func (a *PartAccumulator) Part(i int) Part {
	p := a.parts[i]
	return Part{Index: p.index, Text: p.text.String(), File: p.file}
}

func (a *PartAccumulator) Parts() []Part {
	parts := make([]Part, len(a.parts))
	for i := range parts {
		parts[i] = a.Part(i)
	}
	return parts
}

func (a *PartAccumulator) HasText() bool {
	for _, p := range a.parts {
		if p.text.Len() > 0 {
			return true
		}
	}
	return false
}
