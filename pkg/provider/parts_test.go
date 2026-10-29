package provider

import (
	"reflect"
	"strings"
	"testing"
)

func TestPartAccumulatorInterleavedSparseSnapshots(t *testing.T) {
	var a PartAccumulator
	for _, p := range []Part{{Index: 2, Text: "C"}, {Index: 0, Text: "A"}, {Index: 2, Text: "c"}, {Index: 1, Text: "B"}} {
		a.Add(p)
	}
	before := a.Parts()
	a.Add(Part{Index: 0, Text: "a"})
	if !reflect.DeepEqual(a.Parts(), []Part{{Index: 0, Text: "Aa"}, {Index: 1, Text: "B"}, {Index: 2, Text: "Cc"}}) {
		t.Fatalf("parts: %+v", a.Parts())
	}
	if before[0].Text != "A" {
		t.Fatal("later deltas mutated an earlier snapshot")
	}
	before[0].Text = "changed"
	if a.Part(0).Text != "Aa" {
		t.Fatal("snapshot mutation changed the accumulator")
	}
}

func BenchmarkPartAccumulatorThinking(b *testing.B) {
	delta := Part{Text: strings.Repeat("x", 20)}
	b.ReportAllocs()
	for b.Loop() {
		var a PartAccumulator
		for range 5000 {
			a.Add(delta)
		}
		if len(a.Part(0).Text) != 100000 {
			b.Fatal("lost thinking")
		}
	}
}
