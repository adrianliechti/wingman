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

func (f *fakeSearcher) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{DateFilters: true}
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

func TestResearch_ContinuesBeyondAdvisoryTarget(t *testing.T) {
	first := assistantToolCalls(
		provider.ToolCall{ID: "1", Name: "web_search", Arguments: `{"query":"a"}`},
		provider.ToolCall{ID: "2", Name: "web_search", Arguments: `{"query":"b"}`},
		provider.ToolCall{ID: "3", Name: "web_search", Arguments: `{"query":"c"}`},
	)
	followup := assistantToolCalls(provider.ToolCall{ID: "4", Name: "web_search", Arguments: `{"query":"remaining gap"}`})
	final := provider.AssistantMessage("final answer")
	completer := &fakeCompleter{script: []provider.Completion{{Message: &first}, {Message: &followup}, {Message: &final}}}
	c, err := New(completer, &fakeSearcher{}, WithToolCallTarget(2))
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Research(t.Context(), "question", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final answer" || len(completer.calls) != 3 {
		t.Fatalf("result=%+v completion calls=%d", result, len(completer.calls))
	}
	ids := map[string]string{}
	for _, message := range completer.calls[2].messages {
		if r, ok := message.ToolResult(); ok {
			ids[r.ID] = r.Parts[0].Text
		}
	}
	for _, id := range []string{"1", "2", "3", "4"} {
		if !strings.Contains(ids[id], "body") || strings.Contains(ids[id], "Error:") {
			t.Fatalf("tool %s did not execute: %q", id, ids[id])
		}
	}
	if !strings.Contains(ids["3"], "Prefer answering from the evidence gathered") {
		t.Fatal("missing efficiency hint")
	}
	for _, call := range completer.calls {
		if len(call.options.Tools) == 0 {
			t.Fatal("tools removed after the advisory target")
		}
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
