package features_test

import (
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/adrianliechti/wingman/test/openai/responses"
)

func TestShellHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Shell {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": "Use the shell tool to list the files in the current directory.",
				"tools": []any{
					map[string]any{"type": "shell"},
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireShellCall(t, "openai", openaiResp.Body)
			requireShellCall(t, "wingman", wingmanResp.Body)

			rules := openai.DefaultResponsesResponseRules()
			rules["output"] = harness.FieldPresence
			rules["output.*.call_id"] = harness.FieldPresence
			rules["output.*.action"] = harness.FieldPresence
			rules["tools.*.environment"] = harness.FieldIgnore
			harness.CompareStructure(t, "response", openaiResp.Body, wingmanResp.Body, harness.CompareOption{Rules: rules})
		})
	}
}

func TestShellMultiTurnHTTP(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		if !model.Capabilities.Shell {
			continue
		}

		t.Run(model.Name, func(t *testing.T) {
			body := map[string]any{
				"input": []map[string]any{
					{
						"type": "message",
						"role": "user",
						"content": []map[string]any{
							{"type": "input_text", "text": "Use the shell tool to find out which Go version is installed, then summarize it."},
						},
					},
					{
						"type":    "shell_call",
						"id":      "sh_test_1",
						"call_id": "call_shell_1",
						"status":  "completed",
						"action": map[string]any{
							"commands": []string{"go version"},
						},
					},
					{
						"type":    "shell_call_output",
						"call_id": "call_shell_1",
						"output": []map[string]any{
							{
								"stdout":  "go version go1.26.0 darwin/arm64",
								"stderr":  "",
								"outcome": map[string]any{"type": "exit", "exit_code": 0},
							},
						},
					},
				},
				"tools": []any{
					map[string]any{"type": "shell"},
				},
			}

			openaiResp, wingmanResp := responses.CompareHTTP(t, h, model, body)

			requireOutputText(t, "openai", openaiResp.Body)
			requireOutputText(t, "wingman", wingmanResp.Body)
		})
	}
}

func requireShellCall(t *testing.T, label string, body map[string]any) {
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
		if obj["type"] != "shell_call" && obj["type"] != "local_shell_call" {
			continue
		}

		action, ok := obj["action"].(map[string]any)
		if !ok {
			continue
		}

		if commands, _ := action["commands"].([]any); len(commands) > 0 {
			return
		}
		if command, _ := action["command"].([]any); len(command) > 0 {
			return
		}
	}

	t.Fatalf("[%s] no shell_call with commands found", label)
}

func requireOutputText(t *testing.T, label string, body map[string]any) {
	t.Helper()

	output, ok := body["output"].([]any)
	if !ok {
		t.Fatalf("[%s] output is not an array", label)
	}

	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok || obj["type"] != "message" {
			continue
		}

		content, _ := obj["content"].([]any)
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := p["text"].(string); p["type"] == "output_text" && text != "" {
				return
			}
		}
	}

	t.Fatalf("[%s] no output_text found", label)
}
