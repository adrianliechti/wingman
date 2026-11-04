package openai

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func FuzzStopFilterChunkBoundaries(f *testing.F) {
	for _, seed := range []struct {
		source, stop string
		width        uint8
	}{
		{"safe END hidden", "END", 1},
		{"safe 🚀END hidden", "🚀END", 2},
		{"safe EN", "END", 1},
		{"safe EXN", "END", 2},
		{"ABABABAB", "ABAB", 1},
		{"no stop", "", 1},
		{"", "END", 1},
	} {
		f.Add(seed.source, seed.stop, seed.width)
	}
	f.Fuzz(func(t *testing.T, source, stop string, width uint8) {
		if len(source) > 4096 || len(stop) > 128 || !utf8.ValidString(source) || !utf8.ValidString(stop) {
			t.Skip()
		}
		filter := newStopFilter([]string{stop})
		runes := []rune(source)
		var result strings.Builder
		appendText := func(c *provider.Completion) {
			if c.Message != nil {
				for _, part := range c.Message.Content {
					result.WriteString(part.Text)
				}
			}
		}
		hit := false
		for offset := 0; offset < len(runes); {
			end := min(offset+int(width%32)+1, len(runes))
			chunk := text(string(runes[offset:end]))
			out, matched := filter.filter(chunk)
			// Filtering must not mutate upstream-owned chunks.
			if chunk.Message.Text() != string(runes[offset:end]) {
				t.Fatal("filter mutated input")
			}
			appendText(out)
			if matched {
				hit = true
				break
			}
			offset = end
		}
		if !hit {
			out, _ := filter.filter(&provider.Completion{Status: provider.CompletionStatusCompleted})
			appendText(out)
		}
		want := source
		if stop != "" {
			if before, _, ok := strings.Cut(source, stop); ok {
				want = before
			}
		}
		if result.String() != want {
			t.Fatalf("source=%q stop=%q width=%d: got %q, want %q", source, stop, width, result.String(), want)
		}
	})
}

func TestStopFilterKeepsToolBoundaryAndIncompleteSuffix(t *testing.T) {
	filter := newStopFilter([]string{"END"})
	var acc provider.CompletionAccumulator
	chunks := []*provider.Completion{
		text("safe E"),
		{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "record", Arguments: `{"text":"END"}`})}}},
		text("ND and E"),
		{Status: provider.CompletionStatusIncomplete},
	}
	for _, chunk := range chunks {
		out, hit := filter.filter(chunk)
		if hit {
			t.Fatal("matched across a tool boundary or inside tool arguments")
		}
		acc.Add(*out)
	}
	result := acc.Result()
	if result.Message.Text() != "safe END and E" || result.Status != provider.CompletionStatusIncomplete {
		t.Fatalf("lost text or truncation: %+v", result)
	}
	calls := result.Message.ToolCalls()
	if len(calls) != 1 || calls[0].Arguments != `{"text":"END"}` {
		t.Fatalf("tool arguments changed: %+v", calls)
	}
}
