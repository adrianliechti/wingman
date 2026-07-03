package agent

import (
	"context"
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

type fakeSearcher struct{}

func (f *fakeSearcher) Search(ctx context.Context, q string, o *searcher.SearchOptions) ([]searcher.Result, error) {
	return []searcher.Result{{Source: "https://example.com", Title: "Example", Content: "body"}}, nil
}

func (f *fakeSearcher) Categories() []searcher.Category {
	return nil
}

type completerCall struct {
	messages []provider.Message
	options  *provider.CompleteOptions
}

type fakeCompleter struct {
	calls  []completerCall
	script []provider.Completion
}

func (f *fakeCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	completion := f.script[len(f.calls)]
	f.calls = append(f.calls, completerCall{messages: slices.Clone(messages), options: options})

	return func(yield func(*provider.Completion, error) bool) {
		yield(&completion, nil)
	}
}

func assistantToolCalls(calls ...provider.ToolCall) provider.Message {
	m := provider.Message{Role: provider.MessageRoleAssistant}
	for _, tc := range calls {
		m.Content = append(m.Content, provider.ToolCallContent(tc))
	}
	return m
}

func TestResearch_BudgetOverflowAndFinalize(t *testing.T) {
	first := assistantToolCalls(
		provider.ToolCall{ID: "1", Name: "web_search", Arguments: `{"query":"a"}`},
		provider.ToolCall{ID: "2", Name: "web_search", Arguments: `{"query":"b"}`},
		provider.ToolCall{ID: "3", Name: "web_search", Arguments: `{"query":"c"}`},
	)
	final := provider.AssistantMessage("final answer")

	completer := &fakeCompleter{
		script: []provider.Completion{
			{Message: &first},
			{Message: &final},
		},
	}

	c, err := New(completer, &fakeSearcher{}, WithMaxToolCalls(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.Research(context.Background(), "question", nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if result.Content != "final answer" {
		t.Errorf("content = %q", result.Content)
	}

	if len(completer.calls) != 2 {
		t.Fatalf("completer calls = %d, want 2", len(completer.calls))
	}

	second := completer.calls[1]

	ids := map[string]string{}
	for _, m := range second.messages {
		if r, ok := m.ToolResult(); ok {
			ids[r.ID] = r.Parts[0].Text
		}
	}
	for _, id := range []string{"1", "2", "3"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("missing tool result for call %s", id)
		}
	}
	if !strings.Contains(ids["3"], "budget exhausted") {
		t.Errorf("skipped call result = %q", ids["3"])
	}

	last := second.messages[len(second.messages)-1]
	if last.Role != provider.MessageRoleUser || !strings.Contains(last.Text(), "budget is exhausted") {
		t.Errorf("expected finalize prompt as last message; got %+v", last)
	}

	if len(second.options.Tools) != 0 {
		t.Errorf("final completion should have no tools; got %d", len(second.options.Tools))
	}
}

func TestResearch_StopsWithoutToolCalls(t *testing.T) {
	answer := provider.AssistantMessage("direct answer")

	completer := &fakeCompleter{
		script: []provider.Completion{{Message: &answer}},
	}

	c, err := New(completer, &fakeSearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.Research(context.Background(), "question", nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if result.Content != "direct answer" {
		t.Errorf("content = %q", result.Content)
	}
	if len(completer.calls) != 1 {
		t.Errorf("completer calls = %d, want 1", len(completer.calls))
	}
}
