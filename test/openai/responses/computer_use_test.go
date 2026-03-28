package responses_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

func TestComputerUseHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"model":      "gpt-5.4",
		"truncation": "auto",
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Take a screenshot"},
				},
			},
		},
		"tools": []any{
			map[string]any{"type": "computer"},
		},
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: "gpt-5.4"}, body)

	requireComputerCall(t, "openai", openaiResp.Body)
	requireComputerCall(t, "wingman", wingmanResp.Body)

	rules := openai.DefaultResponsesResponseRules()
	rules["output"] = harness.FieldPresence
	rules["output.*.call_id"] = harness.FieldPresence
	rules["output.*.actions"] = harness.FieldPresence
	rules["output.*.pending_safety_checks"] = harness.FieldIgnore
	harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
}

func TestComputerUseMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	body := map[string]any{
		"model":      "gpt-5.4",
		"truncation": "auto",
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Take a screenshot of the current page"},
				},
			},
			{
				"type":    "computer_call",
				"id":      "cu_test",
				"call_id": "call_test",
				"status":  "completed",
				"actions": []map[string]any{
					{"type": "screenshot"},
				},
			},
			{
				"type":    "computer_call_output",
				"call_id": "call_test",
				"output": map[string]any{
					"type":      "computer_screenshot",
					"image_url": testScreenshotDataURL(),
				},
			},
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": "Now click on the center of the screen"},
				},
			},
		},
		"tools": []any{
			map[string]any{"type": "computer"},
		},
	}

	openaiResp, wingmanResp := compareHTTP(t, h, openai.Model{Name: "gpt-5.4"}, body)

	requireComputerCall(t, "openai", openaiResp.Body)
	requireComputerCall(t, "wingman", wingmanResp.Body)
}

func requireComputerCall(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] == "computer_call" {
			return
		}
	}

	t.Fatalf("[%s] no computer_call output item found", label)
}

// testScreenshotDataURL returns a valid 100x100 PNG as a data URL for testing.
func testScreenshotDataURL() string {
	return "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAGQAAABkCAIAAAD/gAIDAAAAtUlEQVR4nO3QQQkAIADAQMOayUzGsoIfGcLBAowbc21dNvKDj4IFC1YeLFiw8mDBgpUHCxasPFiwYOXBggUrDxYsWHmwYMHKgwULVh4sWLDyYMGClQcLFqw8WLBg5cGCBSsPFixYebBgwcqDBQtWHixYsPJgwYKVBwsWrDxYsGDlwYIFKw8WLFh5sGDByoMFC1YeLFiw8mDBgpUHCxasPFiwYOXBggUrDxYsWHmwYMHKgwXrTQdEba4dBLoLFgAAAABJRU5ErkJggg=="
}
