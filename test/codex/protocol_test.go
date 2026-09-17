package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/harness"
)

func checkExchanges(t *testing.T, exchanges []harness.Exchange, model string) map[string]bool {
	t.Helper()
	emitted, returned := map[string]string{}, map[string]bool{}
	responses := 0
	for i, record := range exchanges {
		if record.Path != "/responses" {
			t.Errorf("exchange %d: unexpected Codex API path %q", i, record.Path)
			continue
		}
		responses++
		if record.Status != 200 {
			t.Errorf("exchange %d: returned %d: %.2000s", i, record.Status, record.Response)
			continue
		}
		var request struct {
			Model  string           `json:"model"`
			Stream bool             `json:"stream"`
			Input  []map[string]any `json:"input"`
		}
		if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
			t.Errorf("exchange %d: invalid request JSON: %v", i, err)
			continue
		}
		if record.Method != "POST" || request.Model != model || !request.Stream {
			t.Errorf("exchange %d: unexpected Responses request (method=%s model=%s stream=%t)", i, record.Method, request.Model, request.Stream)
		}
		for _, item := range request.Input {
			switch item["type"] {
			case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output", "apply_patch_call_output":
				id, _ := item["call_id"].(string)
				if emitted[id] == "" {
					t.Errorf("exchange %d: unmatched tool result %q", i, id)
				}
				returned[id] = true
			}
		}
		if !strings.HasPrefix(record.ResponseHeaders.Get("Content-Type"), "text/event-stream") {
			t.Errorf("exchange %d: expected SSE", i)
			continue
		}
		tools, err := validateStream(record.Response)
		if err != nil {
			t.Errorf("exchange %d: %v", i, err)
		}
		for id, name := range tools {
			if emitted[id] != "" {
				t.Errorf("exchange %d: reused tool-call ID %q", i, id)
			}
			emitted[id] = name
		}
	}
	if responses == 0 {
		t.Error("Codex made no Responses requests through the recorder")
	}
	names := map[string]bool{}
	for id, name := range emitted {
		if !returned[id] {
			t.Errorf("tool call %s (%s) was not returned in a later request", name, id)
		}
		names[name] = true
	}
	t.Logf("validated %d Responses streams and %d tool calls", responses, len(emitted))
	return names
}

// Compare lifecycle, usage and reconstructed tool input, independently of
// provider-specific reasoning events or text/argument chunk boundaries.
func validateStream(raw string) (map[string]string, error) {
	events, err := harness.ParseSSE(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	type itemState struct {
		id, kind string
		partial  strings.Builder
		input    string
		closed   bool
	}
	items := map[float64]*itemState{}
	tools := map[string]string{}
	started, completed := false, false
	for _, event := range events {
		data := event.Data
		if data == nil || data["type"] != event.Event || completed {
			return nil, fmt.Errorf("invalid or out-of-order SSE event %q", event.Event)
		}
		if !started && event.Event != "response.created" {
			return nil, fmt.Errorf("%s before response.created", event.Event)
		}
		index, hasIndex := data["output_index"].(float64)
		state := items[index]
		switch event.Event {
		case "response.created":
			response, _ := data["response"].(map[string]any)
			if started || response["id"] == nil || response["id"] == "" {
				return nil, fmt.Errorf("invalid response.created")
			}
			started = true
		case "response.output_item.added":
			item, _ := data["item"].(map[string]any)
			if !hasIndex || index < 0 || index != float64(int(index)) || state != nil || item == nil {
				return nil, fmt.Errorf("invalid output_item.added index %v", data["output_index"])
			}
			id, _ := item["id"].(string)
			kind, _ := item["type"].(string)
			if id == "" || kind == "" {
				return nil, fmt.Errorf("missing output item identity")
			}
			items[index] = &itemState{id: id, kind: kind}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			if !hasIndex || state == nil || state.closed || data["item_id"] != state.id {
				return nil, fmt.Errorf("tool input delta outside an open item")
			}
			if (event.Event == "response.function_call_arguments.delta" && state.kind != "function_call") || (event.Event == "response.custom_tool_call_input.delta" && state.kind != "custom_tool_call") {
				return nil, fmt.Errorf("tool input delta has the wrong item type")
			}
			fragment, ok := data["delta"].(string)
			if !ok {
				return nil, fmt.Errorf("tool input delta must be a string")
			}
			state.partial.WriteString(fragment)
		case "response.output_item.done":
			item, _ := data["item"].(map[string]any)
			if !hasIndex || state == nil || state.closed || item["id"] != state.id || item["type"] != state.kind {
				return nil, fmt.Errorf("output_item.done does not match an open item")
			}
			state.closed = true
			switch state.kind {
			case "function_call", "custom_tool_call":
				field := "arguments"
				if state.kind == "custom_tool_call" {
					field = "input"
				}
				state.input, _ = item[field].(string)
				if state.partial.Len() > 0 && state.partial.String() != state.input {
					return nil, fmt.Errorf("streamed tool input differs from completed item")
				}
				if state.kind == "function_call" {
					var arguments map[string]any
					if json.Unmarshal([]byte(state.input), &arguments) != nil || arguments == nil {
						return nil, fmt.Errorf("invalid tool JSON")
					}
				}
				id, _ := item["call_id"].(string)
				name, _ := item["name"].(string)
				if id == "" || name == "" || tools[id] != "" {
					return nil, fmt.Errorf("invalid or duplicate tool-call identity")
				}
				tools[id] = name
			}
		case "response.completed":
			response, _ := data["response"].(map[string]any)
			usage, _ := response["usage"].(map[string]any)
			_, inputOK := usage["input_tokens"].(float64)
			_, outputOK := usage["output_tokens"].(float64)
			if response["status"] != "completed" || !inputOK || !outputOK {
				return nil, fmt.Errorf("response did not complete with usage")
			}
			output, _ := response["output"].([]any)
			if len(output) != len(items) {
				return nil, fmt.Errorf("completed output does not match streamed items")
			}
			for i, rawItem := range output {
				item, _ := rawItem.(map[string]any)
				state := items[float64(i)]
				if state == nil || !state.closed || item["id"] != state.id || item["type"] != state.kind {
					return nil, fmt.Errorf("completed output does not match closed item %d", i)
				}
			}
			completed = true
		case "response.failed", "response.incomplete", "error":
			return nil, fmt.Errorf("SSE error: %.2000s", event.Raw)
		}
	}
	if !completed {
		return nil, fmt.Errorf("incomplete Responses stream")
	}
	return tools, nil
}
