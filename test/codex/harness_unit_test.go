package codex

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func textStream() string {
	item := `{"type":"message","id":"msg_fixture","role":"assistant","status":"completed","content":[{"type":"output_text","text":"WINGMAN_E2E_OK","annotations":[]}]}`
	var out strings.Builder
	for _, event := range []struct{ kind, fields string }{
		{"response.created", `"response":{"id":"resp_fixture","object":"response","status":"in_progress","output":[]}`},
		{"response.output_item.added", `"output_index":0,"item":{"type":"message","id":"msg_fixture","role":"assistant","status":"in_progress","content":[]}`},
		{"response.content_part.added", `"output_index":0,"item_id":"msg_fixture","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}`},
		{"response.output_text.delta", `"output_index":0,"item_id":"msg_fixture","content_index":0,"delta":"WINGMAN_E2E_OK"`},
		{"response.output_text.done", `"output_index":0,"item_id":"msg_fixture","content_index":0,"text":"WINGMAN_E2E_OK"`},
		{"response.content_part.done", `"output_index":0,"item_id":"msg_fixture","content_index":0,"part":{"type":"output_text","text":"WINGMAN_E2E_OK","annotations":[]}`},
		{"response.output_item.done", `"output_index":0,"item":` + item},
		{"response.completed", `"response":{"id":"resp_fixture","object":"response","status":"completed","output":[` + item + `],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`},
	} {
		fmt.Fprintf(&out, "event: %s\ndata: {\"type\":%q,%s}\n\n", event.kind, event.kind, event.fields)
	}
	return out.String()
}

func toolStream(kind, name, input string) string {
	field, deltaEvent, doneEvent := "arguments", "response.function_call_arguments.delta", "response.function_call_arguments.done"
	if kind == "custom_tool_call" {
		field, deltaEvent, doneEvent = "input", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done"
	}
	id := "item_" + name
	item := map[string]any{"id": id, "type": kind, "call_id": "call_" + name, "name": name, field: input, "status": "completed"}
	added := map[string]any{"id": id, "type": kind, "call_id": "call_" + name, "name": name, field: "", "status": "in_progress"}
	var out strings.Builder
	for _, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_" + name, "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": added},
		{"type": deltaEvent, "output_index": 0, "item_id": id, "delta": input},
		{"type": doneEvent, "output_index": 0, "item_id": id, field: input},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": map[string]any{"id": "resp_" + name, "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}},
	} {
		data, _ := json.Marshal(event)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	return out.String()
}

// Exercise the real CLI and generated config against a scripted local API.
// This test never calls a paid provider and skips if Codex is absent.
func TestCodexOffline(t *testing.T) {
	binary := codexBinary(t, false)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/responses" {
			t.Errorf("unexpected upstream request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		if req.Header.Get("Authorization") != "Bearer test" || req.Header.Get("Content-Encoding") != "" {
			t.Error("proxy must replace dummy credentials and the CLI must send uncompressed requests")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, textStream())
	}))
	t.Cleanup(upstream.Close)
	actual, exchanges, _ := runCodex(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, "gpt-5.4-mini", "Reply with exactly WINGMAN_E2E_OK. Do not use tools.", "")
	if actual != (outcome{Answer: "WINGMAN_E2E_OK"}) {
		t.Errorf("unexpected CLI outcome: %+v", actual)
	}
	checkExchanges(t, exchanges, "gpt-5.4-mini")
}

func TestCodexOfflineReadEdit(t *testing.T) {
	binary := codexBinary(t, false)
	input := "fixture-" + rand.Text() + "\n"
	patch := "*** Begin Patch\n*** Update File: output.txt\n@@\n-REPLACE_ME\n+" + strings.TrimSpace(input) + "\n*** End Patch"
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var request struct {
			Tools []struct{ Type, Name string }
			Input []map[string]any
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		toolTypes := map[string]string{}
		for _, tool := range request.Tools {
			toolTypes[tool.Name] = tool.Type
		}
		if toolTypes["exec_command"] != "function" || toolTypes["apply_patch"] != "custom" {
			t.Errorf("required CLI tools are missing for a custom model: %v", toolTypes)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		calls++
		switch calls {
		case 1:
			io.WriteString(w, toolStream("function_call", "exec_command", `{"cmd":"cat input.txt output.txt"}`))
		case 2:
			found := false
			for _, item := range request.Input {
				if item["type"] == "function_call_output" && item["call_id"] == "call_exec_command" {
					output, _ := item["output"].(string)
					found = strings.Contains(output, input)
				}
			}
			if !found {
				t.Error("Codex did not return the actual file contents")
			}
			io.WriteString(w, toolStream("custom_tool_call", "apply_patch", patch))
		case 3:
			io.WriteString(w, textStream())
		default:
			t.Errorf("unexpected extra CLI request %d", calls)
		}
	}))
	t.Cleanup(upstream.Close)
	actual, exchanges, fixture := runCodex(t, binary, harness.Endpoint{Name: "offline", BaseURL: upstream.URL + "/v1", APIKey: "test"}, "wingman-fixture", "Read input.txt and update output.txt with apply_patch.", input)
	if actual != (outcome{Answer: "WINGMAN_E2E_OK", Read: true, Edited: true}) {
		t.Errorf("unexpected CLI outcome: %+v", actual)
	}
	tools := checkExchanges(t, exchanges, "wingman-fixture")
	if !reflect.DeepEqual(tools, map[string]bool{"exec_command": true, "apply_patch": true}) {
		t.Errorf("unexpected tool calls: %v", tools)
	}
	data, err := os.ReadFile(filepath.Join(fixture, "output.txt"))
	if err != nil || string(data) != input {
		t.Errorf("Codex did not apply the edit: %q, %v", data, err)
	}
}

func TestValidateStream(t *testing.T) {
	tool := toolStream("function_call", "exec_command", `{"cmd":"cat input.txt"}`)
	for _, tc := range []struct {
		name, stream string
		valid        bool
	}{
		{"text", textStream(), true},
		{"function_tool", tool, true},
		{"custom_tool", toolStream("custom_tool_call", "apply_patch", "*** Begin Patch\n*** End Patch"), true},
		{"malformed_tool_json", toolStream("function_call", "exec_command", `{"cmd":`), false},
		{"mismatched_tool_input", strings.Replace(tool, `"delta":"{\"cmd\":\"cat input.txt\"}"`, `"delta":"{}"`, 1), false},
		{"missing_completion", strings.Split(textStream(), "event: response.completed")[0], false},
		{"duplicate_completion", textStream() + textStream(), false},
		{"missing_usage", strings.Replace(textStream(), `"output_tokens":5`, `"other":5`, 1), false},
		{"unopened_item", strings.Replace(textStream(), `"type":"response.output_item.done","output_index":0`, `"type":"response.output_item.done","output_index":1`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateStream(tc.stream)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}
