package signatures

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type captureCompleter struct {
	messages []provider.Message
	options  *provider.CompleteOptions

	reply *provider.Completion
}

func (c *captureCompleter) Complete(_ context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.messages = messages
		c.options = options
		yield(c.reply, nil)
	}
}

func TestCompleterStripsSignatures(t *testing.T) {
	inner := &captureCompleter{
		reply: &provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ReasoningContent(provider.Reasoning{ID: "rs_2", Summary: "thinking", Signature: "fresh-blob"}),
					provider.ToolCallContent(provider.ToolCall{ID: "call_2::lookup::ZnJlc2gtc2ln", Name: "lookup"}),
					provider.TextContent("answer"),
				},
			},
		},
	}

	messages := []provider.Message{
		provider.UserMessage("hi"),
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Signature: "foreign-blob"}),
				provider.ReasoningContent(provider.Reasoning{ID: "rs_r", Signature: "redacted-blob", Redacted: true}),
				provider.ReasoningContent(provider.Reasoning{ID: "rs_t", Text: "kept thought", Signature: "signed"}),
				provider.CompactionContent(provider.Compaction{ID: "cp_1", Signature: "compaction-blob"}),
				provider.CompactionContent(provider.Compaction{ID: "cp_2", Content: "summary text", Signature: "signed"}),
				provider.ToolCallContent(provider.ToolCall{ID: "call_1::search::c2ln", Name: "search"}),
				provider.TextContent("hello"),
			},
		},
		{
			Role: provider.MessageRoleUser,
			Content: []provider.Content{
				provider.ToolResultContent(provider.ToolResult{ID: "call_1::search::c2ln"}),
			},
		},
	}

	options := &provider.CompleteOptions{
		ReasoningOptions:  &provider.ReasoningOptions{IncludeSignature: true, IncludeSummary: true},
		CompactionOptions: &provider.CompactionOptions{Threshold: 1000},
	}

	var got *provider.Completion
	for completion, err := range FromCompleter(inner).Complete(context.Background(), messages, options) {
		if err != nil {
			t.Fatal(err)
		}
		got = completion
	}

	assistant := inner.messages[1]
	if len(assistant.Content) != 4 {
		t.Fatalf("assistant contents: %+v", assistant.Content)
	}
	if r := assistant.Content[0].Reasoning; r == nil || r.Signature != "" || r.Text != "kept thought" {
		t.Fatalf("reasoning content: %+v", assistant.Content[0].Reasoning)
	}
	if c := assistant.Content[1].Compaction; c == nil || c.Signature != "" || c.Content != "summary text" {
		t.Fatalf("compaction content: %+v", assistant.Content[1].Compaction)
	}
	if call := assistant.Content[2].ToolCall; call == nil || call.ID != "call_1::search" {
		t.Fatalf("tool call: %+v", call)
	}
	if assistant.Content[3].Text != "hello" {
		t.Fatalf("text content: %+v", assistant.Content[3])
	}
	if result := inner.messages[2].Content[0].ToolResult; result == nil || result.ID != "call_1::search" {
		t.Fatalf("tool result: %+v", result)
	}

	if inner.options.ReasoningOptions.IncludeSignature {
		t.Fatal("IncludeSignature must be cleared")
	}
	if !inner.options.ReasoningOptions.IncludeSummary {
		t.Fatal("IncludeSummary must be preserved")
	}
	if inner.options.CompactionOptions != nil {
		t.Fatal("compaction must be disabled when its continuation signature is stripped")
	}
	if !options.ReasoningOptions.IncludeSignature {
		t.Fatal("caller options must not be mutated")
	}
	if options.CompactionOptions == nil {
		t.Fatal("caller compaction options must not be mutated")
	}
	if messages[1].Content[0].Reasoning.Signature != "foreign-blob" {
		t.Fatal("caller messages must not be mutated")
	}
	if messages[1].Content[5].ToolCall.ID != "call_1::search::c2ln" {
		t.Fatal("caller tool id must not be mutated")
	}

	if len(got.Message.Content) != 3 {
		t.Fatalf("reply contents: %+v", got.Message.Content)
	}
	if r := got.Message.Content[0].Reasoning; r == nil || r.Signature != "" || r.Summary != "thinking" {
		t.Fatalf("reply reasoning: %+v", got.Message.Content[0].Reasoning)
	}
	if call := got.Message.Content[1].ToolCall; call == nil || call.ID != "call_2::lookup" {
		t.Fatalf("reply tool call: %+v", call)
	}
}

func TestStripOptionsDisablesCompactionWithoutReasoning(t *testing.T) {
	options := &provider.CompleteOptions{
		CompactionOptions: &provider.CompactionOptions{Threshold: 1000},
	}

	got := stripOptions(options)
	if got == options {
		t.Fatal("options must be copied before modification")
	}
	if got.CompactionOptions != nil {
		t.Fatal("compaction must be disabled")
	}
	if options.CompactionOptions == nil {
		t.Fatal("caller options must not be mutated")
	}
}

func TestScopedTagsAndResolves(t *testing.T) {
	inner := &captureCompleter{
		reply: &provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ReasoningContent(provider.Reasoning{Text: "thinking", Signature: "SIG"}),
					provider.CompactionContent(provider.Compaction{ID: "cp", Signature: "BLOB"}),
					provider.TextContent("answer"),
				},
			},
		},
	}

	scoped := ScopedTo("claude-sonnet-4-6", inner)

	messages := []provider.Message{
		provider.UserMessage("hi"),
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{Text: "own", Signature: "@claude-sonnet-4-6:OWN"}),
			provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: "foreign", Signature: "@gpt-5.4:ENC"}),
			provider.ReasoningContent(provider.Reasoning{Redacted: true, Signature: "@gpt-5.4:RED"}),
			provider.ReasoningContent(provider.Reasoning{Text: "legacy", Signature: "RAW"}),
			provider.CompactionContent(provider.Compaction{Content: "summary", Signature: "@gpt-5.4:CMP"}),
			provider.ToolCallContent(provider.ToolCall{ID: "call_1::search::c2ln", Name: "search", Arguments: "{}"}),
		}},
		provider.ToolMessage("call_1::search::c2ln", "ok"),
	}

	options := &provider.CompleteOptions{
		ReasoningOptions:  &provider.ReasoningOptions{IncludeSignature: true},
		CompactionOptions: &provider.CompactionOptions{Threshold: 1000},
	}

	var out []*provider.Completion
	for completion, err := range scoped.Complete(context.Background(), messages, options) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, completion)
	}

	replayed := inner.messages[1].Content
	if len(replayed) != 5 {
		t.Fatalf("replayed contents: %+v", replayed)
	}
	if replayed[0].Reasoning.Signature != "OWN" {
		t.Errorf("own signature must be unwrapped: %q", replayed[0].Reasoning.Signature)
	}
	if replayed[1].Reasoning.Signature != "" || replayed[1].Reasoning.Summary != "foreign" {
		t.Errorf("foreign signature must be stripped, text kept: %+v", replayed[1].Reasoning)
	}
	if replayed[2].Reasoning.Signature != "RAW" {
		t.Errorf("untagged signature must pass through: %q", replayed[2].Reasoning.Signature)
	}
	if replayed[3].Compaction.Signature != "" || replayed[3].Compaction.Content != "summary" {
		t.Errorf("foreign compaction keeps its summary only: %+v", replayed[3].Compaction)
	}
	if replayed[4].ToolCall.ID != "call_1::search::c2ln" {
		t.Errorf("scoped mode leaves tool ids alone: %q", replayed[4].ToolCall.ID)
	}

	if !inner.options.ReasoningOptions.IncludeSignature || inner.options.CompactionOptions == nil {
		t.Error("scoped mode must not touch options")
	}

	if messages[1].Content[0].Reasoning.Signature != "@claude-sonnet-4-6:OWN" {
		t.Error("input history was mutated")
	}

	reply := out[0].Message.Content
	if reply[0].Reasoning.Signature != "@claude-sonnet-4-6:SIG" {
		t.Errorf("emitted reasoning must be tagged: %q", reply[0].Reasoning.Signature)
	}
	if reply[1].Compaction.Signature != "@claude-sonnet-4-6:BLOB" {
		t.Errorf("emitted compaction must be tagged: %q", reply[1].Compaction.Signature)
	}
}
