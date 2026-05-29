package responses

import (
	"bytes"
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

const grammarModel = "grammar-test-model"

// capturingCompleter records the options/messages it received so a test can
// assert the conversion preserved the tool's grammar/format.
type capturingCompleter struct {
	gotOptions  *provider.CompleteOptions
	gotMessages []provider.Message
}

func (c *capturingCompleter) Complete(_ context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.gotOptions = options
		c.gotMessages = messages

		yield(&provider.Completion{
			Status: provider.CompletionStatusCompleted,
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.TextContent("ok")},
			},
		}, nil)
	}
}

// TestCustomFreeformGrammarToolPassesThroughToProvider asserts that a
// non-apply_patch custom tool with a lark grammar reaches the provider as
// ToolKindCustom with Format populated — not as a degenerated function tool.
func TestCustomFreeformGrammarToolPassesThroughToProvider(t *testing.T) {
	completer := &capturingCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(grammarModel, completer)

	body := []byte(`{
		"model": "` + grammarModel + `",
		"tools": [
			{
				"type": "custom",
				"name": "sql_query",
				"description": "Run a SQL SELECT",
				"format": {
					"type": "grammar",
					"syntax": "lark",
					"definition": "start: \"SELECT\" CNAME"
				}
			}
		],
		"input": "query"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if completer.gotOptions == nil || len(completer.gotOptions.Tools) != 1 {
		t.Fatalf("expected 1 tool reaching provider, got %+v", completer.gotOptions)
	}

	tool := completer.gotOptions.Tools[0]
	if tool.Name != "sql_query" {
		t.Fatalf("expected name=sql_query, got %q", tool.Name)
	}
	if tool.Kind != provider.ToolKindCustom {
		t.Fatalf("expected Kind=custom, got %q", tool.Kind)
	}
	if tool.Format == nil {
		t.Fatal("expected Format populated, got nil — grammar is being lost")
	}
	if tool.Format.Type != "grammar" {
		t.Fatalf("expected Format.Type=grammar, got %q", tool.Format.Type)
	}
	if tool.Format.Syntax != "lark" {
		t.Fatalf("expected Format.Syntax=lark, got %q", tool.Format.Syntax)
	}
	if tool.Format.Definition == "" {
		t.Fatal("expected Format.Definition populated, got empty")
	}
}

// TestApplyPatchCustomToolDoesNotCarryFormat asserts that apply_patch
// registered as custom dispatches as text_editor and Format is not propagated
// (the format is structurally implicit for text editor builtins).
func TestApplyPatchCustomToolDoesNotCarryFormat(t *testing.T) {
	completer := &capturingCompleter{}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(grammarModel, completer)

	body := []byte(`{
		"model": "` + grammarModel + `",
		"tools": [
			{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: x"}}
		],
		"input": "patch"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	tool := completer.gotOptions.Tools[0]
	if tool.Kind != provider.ToolKindTextEditor {
		t.Fatalf("expected apply_patch custom → ToolKindTextEditor, got %q", tool.Kind)
	}
	if tool.Format != nil {
		t.Fatalf("expected Format to be nil for apply_patch text-editor path, got %+v", tool.Format)
	}
}
