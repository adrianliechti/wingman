package computeruse

import (
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestParseCall_OpenAI(t *testing.T) {
	call := ParseCall(`{"call_id":"call_1","actions":[{"type":"click","button":"left","x":10,"y":20}],"pending_safety_checks":[{"id":"sc_1","code":"malicious_instructions","message":"check this"}]}`)

	if call.CallID != "call_1" || len(call.Actions) != 1 {
		t.Fatalf("call: %+v", call)
	}
	if call.Actions[0]["type"] != "click" {
		t.Fatalf("action: %+v", call.Actions[0])
	}
	if len(call.PendingSafetyChecks) != 1 || call.PendingSafetyChecks[0].ID != "sc_1" {
		t.Fatalf("safety checks: %+v", call.PendingSafetyChecks)
	}
}

func TestParseCall_AnthropicInput(t *testing.T) {
	call := ParseCall(`{"action":"left_click","coordinate":[10,20]}`)

	if len(call.Actions) != 1 {
		t.Fatalf("actions: %+v", call.Actions)
	}

	want := map[string]any{"type": "click", "button": "left", "x": 10, "y": 20}
	if !reflect.DeepEqual(call.Actions[0], want) {
		t.Fatalf("action: %+v, want %+v", call.Actions[0], want)
	}
}

func TestAnthropicInput_Passthrough(t *testing.T) {
	input := AnthropicInput(`{"action":"screenshot"}`)

	if input["action"] != "screenshot" {
		t.Fatalf("input: %+v", input)
	}
}

func TestAnthropicInput_FromActions(t *testing.T) {
	input := AnthropicInput(`{"call_id":"call_1","actions":[{"type":"keypress","keys":["ctrl","s"]}]}`)

	if input["action"] != "key" || input["text"] != "ctrl+s" {
		t.Fatalf("input: %+v", input)
	}
}

func TestActionMappingRoundTrips(t *testing.T) {
	inputs := []map[string]any{
		{"action": "screenshot"},
		{"action": "left_click", "coordinate": []any{10.0, 20.0}},
		{"action": "right_click", "coordinate": []any{10.0, 20.0}},
		{"action": "middle_click", "coordinate": []any{10.0, 20.0}},
		{"action": "double_click", "coordinate": []any{10.0, 20.0}},
		{"action": "mouse_move", "coordinate": []any{5.0, 6.0}},
		{"action": "key", "text": "ctrl+shift+p"},
		{"action": "type", "text": "hello"},
	}

	for _, input := range inputs {
		action, ok := FromAnthropicInput(input)
		if !ok {
			t.Fatalf("FromAnthropicInput(%+v) not convertible", input)
		}

		back, ok := ToAnthropicInput(action)
		if !ok {
			t.Fatalf("ToAnthropicInput(%+v) not convertible", action)
		}

		if back["action"] != input["action"] {
			t.Errorf("round trip %v: got %v", input["action"], back["action"])
		}

		if text, has := input["text"]; has && back["text"] != text {
			t.Errorf("round trip %v text: got %v, want %v", input["action"], back["text"], text)
		}
	}
}

func TestScrollMapping(t *testing.T) {
	input := map[string]any{"action": "scroll", "coordinate": []any{100.0, 200.0}, "scroll_direction": "down", "scroll_amount": 3.0}

	action, ok := FromAnthropicInput(input)
	if !ok {
		t.Fatal("scroll not convertible")
	}
	if action["scroll_y"] != 3*scrollPixelsPerClick {
		t.Fatalf("scroll_y: %v", action["scroll_y"])
	}

	back, ok := ToAnthropicInput(action)
	if !ok {
		t.Fatal("scroll not convertible back")
	}
	if back["scroll_direction"] != "down" || back["scroll_amount"] != 3 {
		t.Fatalf("back: %+v", back)
	}
}

func TestDragMapping(t *testing.T) {
	input := map[string]any{"action": "left_click_drag", "start_coordinate": []any{1.0, 2.0}, "coordinate": []any{3.0, 4.0}}

	action, ok := FromAnthropicInput(input)
	if !ok || action["type"] != "drag" {
		t.Fatalf("action: %+v", action)
	}

	data, _ := ParseCall(Call{Actions: []map[string]any{action}}.Args()).Actions[0]["path"].([]any)
	if len(data) != 2 {
		t.Fatalf("path: %+v", data)
	}

	back, ok := ToAnthropicInput(ParseCall(Call{Actions: []map[string]any{action}}.Args()).Actions[0])
	if !ok {
		t.Fatal("drag not convertible back")
	}

	if !reflect.DeepEqual(back["start_coordinate"], []int{1, 2}) || !reflect.DeepEqual(back["coordinate"], []int{3, 4}) {
		t.Fatalf("back: %+v", back)
	}
}

func TestUnsupportedActions(t *testing.T) {
	if _, ok := FromAnthropicInput(map[string]any{"action": "cursor_position"}); ok {
		t.Error("cursor_position should not convert")
	}
	if _, ok := ToAnthropicInput(map[string]any{"type": "click", "button": "back"}); ok {
		t.Error("back button should not convert")
	}
}

func TestFunctionTool(t *testing.T) {
	display := &provider.Display{Width: 1280, Height: 800}

	openai := FunctionTool(provider.Tool{Kind: provider.ToolKindComputer, Name: Name, Dialect: DialectOpenAI, Display: display})
	if openai.Kind != provider.ToolKindFunction || openai.Name != Name || openai.Parameters == nil {
		t.Fatalf("openai tool: %+v", openai)
	}

	anthropic := FunctionTool(provider.Tool{Kind: provider.ToolKindComputer, Name: Name, Dialect: DialectAnthropic, Display: display})
	if anthropic.Kind != provider.ToolKindFunction || anthropic.Name != Name || anthropic.Parameters == nil {
		t.Fatalf("anthropic tool: %+v", anthropic)
	}

	if reflect.DeepEqual(openai.Parameters, anthropic.Parameters) {
		t.Fatal("dialect schemas should differ")
	}
}

// schemaEnum pulls the enum of a top-level string property out of an emulated
// tool schema.
func schemaEnum(t *testing.T, params map[string]any, property string) []string {
	t.Helper()

	props, _ := params["properties"].(map[string]any)
	field, _ := props[property].(map[string]any)
	values, _ := field["enum"].([]string)

	if len(values) == 0 {
		t.Fatalf("no enum on property %q", property)
	}

	return values
}

func requireCovers(t *testing.T, got []string, want []string, label string) {
	t.Helper()

	have := map[string]bool{}
	for _, v := range got {
		have[v] = true
	}

	var missing []string
	for _, v := range want {
		if !have[v] {
			missing = append(missing, v)
		}
	}

	if len(missing) > 0 {
		t.Errorf("%s is missing %v (have %v)", label, missing, got)
	}

	allowed := map[string]bool{}
	for _, v := range want {
		allowed[v] = true
	}

	var extra []string
	for _, v := range got {
		if !allowed[v] {
			extra = append(extra, v)
		}
	}

	if len(extra) > 0 {
		t.Errorf("%s advertises %v, which the native tool does not accept", label, extra)
	}
}

// The emulation keeps the client's dialect, so its schema has to advertise the
// same actions the native Anthropic computer toolset accepts — a narrower enum
// silently removes capability the client believes it registered.
// Source: computer_toolset_20260801 (docs, 2026-08-19).
func TestFunctionToolAnthropicCoversNativeActions(t *testing.T) {
	tool := FunctionTool(provider.Tool{Kind: provider.ToolKindComputer, Name: Name, Dialect: DialectAnthropic})

	requireCovers(t, schemaEnum(t, tool.Parameters, "action"), []string{
		"screenshot", "zoom", "left_click", "right_click", "middle_click",
		"double_click", "triple_click", "left_click_drag", "mouse_move",
		"left_mouse_down", "left_mouse_up", "cursor_position", "scroll",
		"type", "key", "hold_key", "wait",
	}, "anthropic action enum")

	props, _ := tool.Parameters["properties"].(map[string]any)

	for _, param := range []string{"coordinate", "start_coordinate", "text", "region", "repeat", "scroll_direction", "scroll_amount", "duration"} {
		if _, ok := props[param]; !ok {
			t.Errorf("anthropic schema is missing the %q parameter", param)
		}
	}
}

// Source: ResponseComputerToolCallAction union, openai-go v3.
func TestFunctionToolOpenAICoversNativeActions(t *testing.T) {
	tool := FunctionTool(provider.Tool{Kind: provider.ToolKindComputer, Name: Name, Dialect: DialectOpenAI})

	props, _ := tool.Parameters["properties"].(map[string]any)
	actions, _ := props["actions"].(map[string]any)
	items, _ := actions["items"].(map[string]any)

	requireCovers(t, schemaEnum(t, items, "type"), []string{
		"screenshot", "click", "double_click", "move", "drag",
		"keypress", "type", "scroll", "wait",
	}, "openai action enum")

	requireCovers(t, schemaEnum(t, items, "button"), []string{
		"left", "right", "wheel", "back", "forward",
	}, "openai button enum")
}
