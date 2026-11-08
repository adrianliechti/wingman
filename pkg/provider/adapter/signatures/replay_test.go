package signatures

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

type replayCompleter struct {
	calls [][]provider.Message
	run   func(context.Context, int, func(*provider.Completion, error) bool)
}

func (c *replayCompleter) Complete(ctx context.Context, messages []provider.Message, _ *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.calls = append(c.calls, messages)
		c.run(ctx, len(c.calls), yield)
	}
}

func TestReasoningReplayRecoveryBoundaries(t *testing.T) {
	rejection := &provider.ProviderError{Code: 400, Type: "invalid_encrypted_content", Message: "Encrypted content could not be verified"}
	for _, tc := range []struct {
		name     string
		err      error
		prefix   bool
		cancel   bool
		signed   bool
		repeat   bool
		attempts int
	}{
		{name: "encrypted content", err: rejection, signed: true, attempts: 2},
		{name: "Claude thinking signature", err: &provider.ProviderError{Code: 400, Type: "invalid_request_error", Message: "Invalid signature in thinking block"}, signed: true, attempts: 2},
		{name: "Bedrock thinking verification", err: &provider.ProviderError{Code: 400, Message: "Thinking signature verification failed"}, signed: true, attempts: 2},
		{name: "trailing thinking", err: errors.New("The final block in an assistant message cannot be `thinking`"), signed: true, attempts: 2},
		{name: "required following item", err: errors.New("reasoning was provided without its required following item"), signed: true, attempts: 2},
		{name: "bounded", err: rejection, signed: true, repeat: true, attempts: 2},
		{name: "unsigned history", err: rejection, attempts: 1},
		{name: "after first completion", err: rejection, signed: true, prefix: true, attempts: 1},
		{name: "cancelled", err: rejection, signed: true, cancel: true, attempts: 1},
		{name: "auth", err: &provider.ProviderError{Code: 401, Type: "invalid_encrypted_content", Message: rejection.Message}, signed: true, attempts: 1},
		{name: "rate limit", err: &provider.ProviderError{Code: 429, Type: "invalid_encrypted_content", Message: rejection.Message}, signed: true, attempts: 1},
		{name: "server", err: &provider.ProviderError{Code: 500, Type: "invalid_encrypted_content", Message: rejection.Message}, signed: true, attempts: 1},
		{name: "unrelated input", err: &provider.ProviderError{Code: 400, Message: "Invalid tool schema"}, signed: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			signature := ""
			if tc.signed {
				signature = "@alias:old"
			}
			messages := []provider.Message{provider.UserMessage("go"), {Role: provider.MessageRoleAssistant, Content: []provider.Content{
				provider.ReasoningContent(provider.Reasoning{ID: "rs", Content: provider.TextParts("signed text"), Summary: provider.TextParts("summary"), Signature: signature}),
				provider.ToolCallContent(provider.ToolCall{ID: "call", Name: "lookup", Arguments: `{}`}),
			}}, provider.ToolMessage("call", "42")}
			original := fmt.Sprintf("%#v", *messages[1].Content[0].Reasoning)
			inner := &replayCompleter{run: func(_ context.Context, attempt int, yield func(*provider.Completion, error) bool) {
				if tc.cancel {
					cancel()
				}
				if tc.prefix && !yield(&provider.Completion{ID: "started"}, nil) {
					return
				}
				if attempt == 1 || tc.repeat {
					yield(nil, tc.err)
					return
				}
				yield(&provider.Completion{Status: provider.CompletionStatusCompleted, Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: "new"}), provider.TextContent("42")}}}, nil)
			}}
			var failure error
			var output *provider.Completion
			for chunk, err := range ScopedTo("alias", inner).Complete(ctx, messages, nil) {
				output, failure = chunk, err
			}
			if len(inner.calls) != tc.attempts {
				t.Fatalf("attempts=%d want %d", len(inner.calls), tc.attempts)
			}
			if tc.attempts == 2 && !tc.repeat {
				if failure != nil || output.Status != provider.CompletionStatusCompleted {
					t.Fatalf("recovery failed: %v", failure)
				}
				if output.Message.Content[0].Reasoning.Signature != "@alias:new" {
					t.Fatal("new output signature lost")
				}
			} else if failure != tc.err {
				t.Fatalf("original failure lost: %v", failure)
			}
			if tc.attempts == 2 {
				replay := inner.calls[1]
				r := replay[1].Content[0].Reasoning
				if r.Signature != "" || r.Text() != "signed text" || r.SummaryText() != "summary" {
					t.Fatalf("bad retry reasoning: %+v", r)
				}
				if !reflect.DeepEqual(replay[1].Content[1], messages[1].Content[1]) || !reflect.DeepEqual(replay[2], messages[2]) {
					t.Fatal("tool history changed")
				}
			}
			if original != fmt.Sprintf("%#v", *messages[1].Content[0].Reasoning) {
				t.Fatal("caller history mutated")
			}
		})
	}
}

func TestReasoningRecoveryPreservesCompactionAndDropsOpaqueOnlyTurns(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: "opaque"})}},
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{Signature: "redacted", Redacted: true})}},
		{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.CompactionContent(provider.Compaction{ID: "cp", Signature: "required-history"}), provider.TextContent("answer")}},
	}
	stripped, changed := withoutReasoningSignatures(messages)
	if !changed || len(stripped) != 1 || !reflect.DeepEqual(stripped[0], messages[2]) {
		t.Fatalf("unsafe recovery: %+v", stripped)
	}
	inner := &replayCompleter{run: func(_ context.Context, _ int, yield func(*provider.Completion, error) bool) {
		yield(nil, &provider.ProviderError{Code: 400, Type: "invalid_encrypted_content"})
	}}
	for range ScopedTo("alias", inner).Complete(t.Context(), messages[2:], nil) {
	}
	if len(inner.calls) != 1 {
		t.Fatal("compaction rejection retried without removable reasoning")
	}
}
