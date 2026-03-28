package responses_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestApplyPatchCreateHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "Create a file called hello.py with a simple hello world program that prints 'Hello, World!'",
				"tools": []any{
					map[string]any{"type": "apply_patch"},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireApplyPatchCall(t, "openai", openaiResp.Body)
			requireApplyPatchCall(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["output.*.call_id"] = harness.FieldPresence
			rules["output.*.operation.diff"] = harness.FieldIgnore
			rules["output.*.operation.path"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestApplyPatchEditHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "The file hello.py contains:\nprint(\"Hello, world!\")\n\nChange it to print Goodbye instead. Apply the patch directly without viewing."},
						},
					},
				},
				"tools": []any{
					map[string]any{"type": "apply_patch"},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireApplyPatchCall(t, "openai", openaiResp.Body)
			requireApplyPatchCall(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["output.*.call_id"] = harness.FieldPresence
			rules["output.*.operation.diff"] = harness.FieldIgnore
			rules["output.*.operation.path"] = harness.FieldPresence
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestApplyPatchSSE(t *testing.T) {
	h := openai.New(t)
	ctx := context.Background()

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			openaiBody := withModel(map[string]any{
				"input":  "Create a file called test.py with print('test')",
				"stream": true,
				"tools":  []any{map[string]any{"type": "apply_patch"}},
			}, h.ReferenceModel)

			wingmanBody := withModel(map[string]any{
				"input":  "Create a file called test.py with print('test')",
				"stream": true,
				"tools":  []any{map[string]any{"type": "apply_patch"}},
			}, model.Name)

			openaiEvents, err := h.Client.PostSSE(ctx, h.OpenAI, "/responses", openaiBody)
			if err != nil {
				t.Fatalf("openai SSE request failed: %v", err)
			}

			wingmanEvents, err := h.Client.PostSSE(ctx, h.Wingman, "/responses", wingmanBody)
			if err != nil {
				t.Fatalf("wingman SSE request failed: %v", err)
			}

			requireApplyPatchSSEEvent(t, "openai", openaiEvents)
			requireApplyPatchSSEEvent(t, "wingman", wingmanEvents)
		})
	}
}

func TestApplyPatchMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.TextEditor {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Create hello.py with print(\"hi\")"},
						},
					},
					{
						"type":    "apply_patch_call",
						"id":      "apc_test",
						"call_id": "call_test",
						"status":  "completed",
						"operation": map[string]any{
							"type": "create_file",
							"path": "hello.py",
							"diff": "+print(\"hi\")\n",
						},
					},
					{
						"type":    "apply_patch_call_output",
						"call_id": "call_test",
						"output":  "File created successfully",
						"status":  "completed",
					},
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Now change it to print bye. Apply the patch directly."},
						},
					},
				},
				"tools": []any{
					map[string]any{"type": "apply_patch"},
				},
			}

			openaiResp, wingmanResp := compareHTTP(t, h, model, body)

			requireApplyPatchCall(t, "openai", openaiResp.Body)
			requireApplyPatchCall(t, "wingman", wingmanResp.Body)

			// Verify the operation is an update_file
			requireApplyPatchOperationType(t, "openai", openaiResp.Body, "update_file")
			requireApplyPatchOperationType(t, "wingman", wingmanResp.Body, "update_file")
		})
	}
}

func requireApplyPatchOperationType(t *testing.T, label string, body map[string]any, opType string) {
	t.Helper()

	output, _ := body["output"].([]any)
	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "apply_patch_call" {
			op, _ := obj["operation"].(map[string]any)
			if op["type"] == opType {
				return
			}
		}
	}

	t.Errorf("[%s] no apply_patch_call with operation type %q found", label, opType)
}

func requireApplyPatchCall(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "apply_patch_call" {
			op, ok := obj["operation"].(map[string]any)
			if !ok {
				t.Errorf("[%s] apply_patch_call has no operation", label)
				return
			}
			if op["path"] == nil || op["path"] == "" {
				t.Errorf("[%s] apply_patch_call operation has no path", label)
			}
			if op["diff"] == nil || op["diff"] == "" {
				t.Errorf("[%s] apply_patch_call operation has no diff", label)
			}
			return
		}
	}

	t.Fatalf("[%s] no apply_patch_call output item found", label)
}

func requireApplyPatchSSEEvent(t *testing.T, label string, events []*harness.SSEEvent) {
	t.Helper()

	for _, e := range events {
		if e.Data == nil {
			continue
		}

		itemType, _ := e.Data["type"].(string)
		if itemType != "response.output_item.added" && itemType != "response.output_item.done" {
			continue
		}

		item, ok := e.Data["item"].(map[string]any)
		if !ok {
			continue
		}

		if item["type"] == "apply_patch_call" {
			return
		}
	}

	t.Fatalf("[%s] no apply_patch_call SSE event found", label)
}
