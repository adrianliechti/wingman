package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

// wireItem captures an output_item.done payload's item object for assertions.
type wireItem struct {
	eventType string
	itemType  string
	operation *ApplyPatchOperation
	text      string
	name      string
	input     string
}

// routeApplyPatch replicates the handler's streaming event routing for the
// arms relevant to apply_patch + text, mirroring handler_responses.go exactly.
// It returns the ordered list of output_item.done items the client would see.
func routeApplyPatch(t *testing.T, opts responseOutputOptions, completions []provider.Completion) []wireItem {
	t.Helper()

	var items []wireItem

	acc := NewStreamingAccumulator(func(event StreamEvent) error {
		switch event.Type {
		case StreamEventOutputItemDone:
			// message text item
			items = append(items, wireItem{eventType: "output_item.done", itemType: "message", text: event.Text})

		case StreamEventFunctionCallDone:
			call := provider.ToolCall{ID: event.ToolCallID, Name: event.ToolCallName, Arguments: event.Arguments}

			switch opts.kindOf(event.ToolCallName) {
			case provider.ToolKindCustom:
				item := toolCallToCustomToolCall(call, "completed")
				items = append(items, wireItem{eventType: "output_item.done", itemType: "custom_tool_call", name: item.Name, input: item.Input})
			case provider.ToolKindTextEditor:
				item := toolCallToApplyPatchCall(call, "completed")
				op := item.Operation
				items = append(items, wireItem{eventType: "output_item.done", itemType: "apply_patch_call", operation: &op})
			default:
				items = append(items, wireItem{eventType: "output_item.done", itemType: "function_call"})
			}
		}
		return nil
	})

	for _, c := range completions {
		if err := acc.Add(c); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	return items
}

// applyPatchCall builds the realistic two-emit upstream sequence the OpenAI
// responder produces for one apply_patch call: an added event with no args,
// followed by a done event carrying the full {type,path,diff} operation.
func applyPatchCall(id, opType, path, diff string) []provider.Completion {
	args, _ := json.Marshal(map[string]any{"type": opType, "path": path, "diff": diff})
	return []provider.Completion{
		{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: id, Name: "apply_patch"}),
		}}},
		{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
			provider.ToolCallContent(provider.ToolCall{ID: id, Name: "apply_patch", Arguments: string(args)}),
		}}},
	}
}

func textChunk(s string) provider.Completion {
	return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.TextContent(s),
	}}}
}

func TestApplyPatchSingle(t *testing.T) {
	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeApplyPatch},
	}}
	items := routeApplyPatch(t, opts, applyPatchCall("call_1", "update_file", "main.go", "@@\n-old\n+new\n"))

	var patches []wireItem
	for _, it := range items {
		if it.itemType == "apply_patch_call" {
			patches = append(patches, it)
		}
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 apply_patch_call, got %d (%+v)", len(patches), items)
	}
	if patches[0].operation.Diff == "" || patches[0].operation.Path == "" {
		t.Fatalf("apply_patch operation missing diff/path: %+v", patches[0].operation)
	}
}

func TestApplyPatchMultiple(t *testing.T) {
	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeApplyPatch},
	}}
	var comps []provider.Completion
	comps = append(comps, applyPatchCall("call_1", "update_file", "a.go", "@@\n-a\n+A\n")...)
	comps = append(comps, applyPatchCall("call_2", "update_file", "b.go", "@@\n-b\n+B\n")...)
	comps = append(comps, applyPatchCall("call_3", "create_file", "c.go", "+C\n")...)

	items := routeApplyPatch(t, opts, comps)

	var patches []wireItem
	for _, it := range items {
		if it.itemType == "apply_patch_call" {
			patches = append(patches, it)
		}
	}
	if len(patches) != 3 {
		t.Fatalf("expected 3 apply_patch_calls, got %d (%+v)", len(patches), items)
	}
	for i, p := range patches {
		if p.operation.Path == "" || p.operation.Type == "" {
			t.Fatalf("patch %d missing operation fields: %+v", i, p.operation)
		}
		if p.operation.Type != "create_file" && p.operation.Diff == "" {
			t.Fatalf("patch %d missing diff: %+v", i, p.operation)
		}
	}
}

func TestApplyPatchMixedTextThenPatch(t *testing.T) {
	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeApplyPatch},
	}}
	var comps []provider.Completion
	comps = append(comps, textChunk("Sure, I'll update the file."))
	comps = append(comps, applyPatchCall("call_1", "update_file", "main.go", "@@\n-old\n+new\n")...)

	items := routeApplyPatch(t, opts, comps)
	assertOnePatchWithDiff(t, items)
}

func TestApplyPatchMixedPatchThenText(t *testing.T) {
	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeApplyPatch},
	}}
	var comps []provider.Completion
	comps = append(comps, applyPatchCall("call_1", "update_file", "main.go", "@@\n-old\n+new\n")...)
	comps = append(comps, textChunk("Done! I updated the file."))

	items := routeApplyPatch(t, opts, comps)
	assertOnePatchWithDiff(t, items)
}

// Codex registers apply_patch as a custom (freeform grammar) tool — output
// must be wrapped as custom_tool_call with the *** Begin Patch envelope.
func TestApplyPatchAsCustomToolCall(t *testing.T) {
	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeCustom, Name: "apply_patch"},
	}}
	items := routeApplyPatch(t, opts, applyPatchCall("call_1", "update_file", "main.go", "@@\n-old\n+new\n"))

	var customs []wireItem
	for _, it := range items {
		if it.itemType == "custom_tool_call" {
			customs = append(customs, it)
		}
	}
	if len(customs) != 1 {
		t.Fatalf("expected 1 custom_tool_call, got %d (%+v)", len(customs), items)
	}
	if customs[0].name != "apply_patch" {
		t.Fatalf("expected custom_tool_call name=apply_patch, got %q", customs[0].name)
	}
	if !strings.Contains(customs[0].input, "*** Begin Patch") ||
		!strings.Contains(customs[0].input, "*** Update File: main.go") ||
		!strings.Contains(customs[0].input, "*** End Patch") {
		t.Fatalf("custom_tool_call input missing envelope markers: %q", customs[0].input)
	}
}

func TestCodexCustomApplyPatchHTTPRoundTrip(t *testing.T) {
	const (
		modelID    = "codex-test-model"
		priorCall  = "call_prior_patch"
		nextCall   = "call_next_patch"
		priorPatch = "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch\n"
	)

	completer := &codexApplyPatchCompleter{
		t: t,
		wantPriorCall: provider.ToolCall{
			ID:   priorCall,
			Name: "apply_patch",
			Arguments: `{
				"type": "update_file",
				"path": "main.go",
				"diff": "@@\n-old\n+new\n"
			}`,
		},
		nextCall: provider.ToolCall{
			ID:        nextCall,
			Name:      "apply_patch",
			Arguments: `{"type":"update_file","path":"main.go","diff":"@@\n-new\n+newer\n"}`,
		},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"instructions": "You are Codex.",
		"stream": false,
		"tools": [
			{
				"type": "custom",
				"name": "apply_patch",
				"description": "Use the apply_patch tool to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON.",
				"format": {
					"type": "grammar",
					"syntax": "lark",
					"definition": "start: begin_patch hunk+ end_patch"
				}
			}
		],
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "Update main.go."}]
			},
			{
				"type": "custom_tool_call",
				"id": "ctc_prior_patch",
				"call_id": "` + priorCall + `",
				"status": "completed",
				"name": "apply_patch",
				"input": ` + mustJSON(t, priorPatch) + `
			},
			{
				"type": "custom_tool_call_output",
				"call_id": "` + priorCall + `",
				"name": "apply_patch",
				"output": "patch applied"
			},
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "Now change new to newer."}]
			}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !completer.called {
		t.Fatal("expected completer to be called")
	}

	var resp struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, rec.Body.String())
	}

	if len(resp.Output) != 1 || resp.Output[0]["type"] != string(ResponseOutputTypeCustomToolCall) {
		t.Fatalf("expected one custom_tool_call output, got %+v", resp.Output)
	}

	call := resp.Output[0]
	if call["call_id"] != nextCall || call["name"] != "apply_patch" || call["status"] != "completed" {
		t.Fatalf("unexpected custom_tool_call fields: %+v", call)
	}
	input, _ := call["input"].(string)
	if !strings.Contains(input, "*** Begin Patch") ||
		!strings.Contains(input, "*** Update File: main.go") ||
		!strings.Contains(input, "@@\n-new\n+newer\n") ||
		!strings.Contains(input, "*** End Patch") {
		t.Fatalf("custom_tool_call input is not a Codex patch envelope: %q", input)
	}
}

type codexApplyPatchCompleter struct {
	t *testing.T

	wantPriorCall provider.ToolCall
	nextCall      provider.ToolCall
	called        bool
}

func (c *codexApplyPatchCompleter) Complete(_ context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		c.called = true
		c.assertCodexRequest(messages, options)

		yield(&provider.Completion{
			ID:     "resp_codex_patch",
			Model:  "codex-test-model",
			Status: provider.CompletionStatusCompleted,
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(c.nextCall),
				},
			},
		}, nil)
	}
}

func (c *codexApplyPatchCompleter) assertCodexRequest(messages []provider.Message, options *provider.CompleteOptions) {
	c.t.Helper()

	if options == nil {
		c.t.Fatal("expected complete options")
	}
	if len(options.Tools) != 1 {
		c.t.Fatalf("expected one tool, got %+v", options.Tools)
	}
	if options.Tools[0].Name != "apply_patch" || options.Tools[0].Kind != provider.ToolKindTextEditor {
		c.t.Fatalf("expected Codex custom apply_patch to dispatch as text editor, got %+v", options.Tools[0])
	}

	var sawPriorCall bool
	var sawPriorOutput bool
	for _, msg := range messages {
		for _, content := range msg.Content {
			if content.ToolCall != nil && content.ToolCall.ID == c.wantPriorCall.ID {
				sawPriorCall = true
				if content.ToolCall.Name != c.wantPriorCall.Name || !sameJSON(c.t, content.ToolCall.Arguments, c.wantPriorCall.Arguments) {
					c.t.Fatalf("prior custom_tool_call converted incorrectly: got %+v want %+v", *content.ToolCall, c.wantPriorCall)
				}
			}
			if content.ToolResult != nil && content.ToolResult.ID == c.wantPriorCall.ID {
				sawPriorOutput = true
				if len(content.ToolResult.Parts) != 1 || content.ToolResult.Parts[0].Text != "patch applied" {
					c.t.Fatalf("prior custom_tool_call_output converted incorrectly: %+v", content.ToolResult.Parts)
				}
			}
		}
	}
	if !sawPriorCall {
		c.t.Fatalf("prior custom_tool_call %q was not passed to provider: %+v", c.wantPriorCall.ID, messages)
	}
	if !sawPriorOutput {
		c.t.Fatalf("prior custom_tool_call_output %q was not passed to provider: %+v", c.wantPriorCall.ID, messages)
	}
}

func sameJSON(t *testing.T, got string, want string) bool {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON %q: %v", got, err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON %q: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON string: %v", err)
	}
	return string(data)
}

func assertOnePatchWithDiff(t *testing.T, items []wireItem) {
	t.Helper()
	var patches []wireItem
	for _, it := range items {
		if it.itemType == "apply_patch_call" {
			patches = append(patches, it)
		}
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 apply_patch_call, got %d (%+v)", len(patches), items)
	}
	if patches[0].operation.Diff == "" || patches[0].operation.Path == "" {
		t.Fatalf("apply_patch operation missing diff/path: %+v", patches[0].operation)
	}
}
