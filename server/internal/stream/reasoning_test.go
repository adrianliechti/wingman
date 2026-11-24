package stream

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
)

type scripted []provider.Completion

func (s scripted) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for i := range s {
			if !yield(&s[i], nil) {
				return
			}
		}
	}
}

func delta(parts ...provider.Content) provider.Completion {
	return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: parts}}
}

func reasoning(r provider.Reasoning) provider.Content { return provider.ReasoningContent(r) }

func TestReasoningBufferStreamsText(t *testing.T) {
	summary := func(text string) []provider.Part { return []provider.Part{{Text: text}} }
	for _, tc := range []struct {
		name      string
		completer provider.Completer
	}{
		{"stripped signature ends the item", signatures.FromCompleter(scripted{
			delta(reasoning(provider.Reasoning{ID: "rs_1", Summary: summary("plan")})),
			delta(reasoning(provider.Reasoning{ID: "rs_1", Signature: "enc"})),
			delta(provider.TextContent("Hel")),
			delta(provider.TextContent("lo")),
		})},
		{"stripped realm with unsigned upstream reasoning", signatures.FromCompleter(scripted{
			delta(reasoning(provider.Reasoning{ID: "rs_1", Summary: summary("plan")})),
			delta(reasoning(provider.Reasoning{ID: "rs_1"})),
			delta(provider.TextContent("Hel")),
			delta(provider.TextContent("lo")),
			{ReasoningSnapshots: provider.ReasoningSnapshots{"rs_1": {ID: "rs_1"}}},
		})},
		{"signature before summary deltas", scripted{
			delta(reasoning(provider.Reasoning{ID: "t_1", Signature: "sig", Summary: summary("first")})),
			delta(reasoning(provider.Reasoning{ID: "t_1", Summary: summary(" continued")})),
			delta(provider.TextContent("Hel")),
			delta(provider.TextContent("lo")),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b ReasoningBuffer
			var text string
			for c, err := range tc.completer.Complete(t.Context(), nil, nil) {
				if err != nil {
					t.Fatal(err)
				}
				ready := b.Add(*c)
				if c.Message != nil && c.Message.Text() != "" && (provider.Message{Content: ready}).Text() != c.Message.Text() {
					t.Fatalf("answer delta %q held until a later completion", c.Message.Text())
				}
				for _, p := range ready {
					text += p.Text
				}
			}
			if text != "Hello" {
				t.Fatalf("text held until the stream ended: streamed %q", text)
			}
			if rest := b.Finish(); len(rest) != 0 {
				t.Fatalf("unreleased content: %+v", rest)
			}
		})
	}
}
