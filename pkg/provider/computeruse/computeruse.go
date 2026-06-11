// Package computeruse bridges the two model-native computer-use tool
// dialects: OpenAI's computer tool (computer_call items with batched actions)
// and Anthropic's computer tool (one action per tool_use).
//
// A computer tool keeps the dialect of the client that registered it
// (provider.Tool.Dialect — the tool is named "computer" in both dialects, so
// the name cannot carry it). Backends with a native tool of the same dialect
// use it directly; all other backends emulate the tool as a plain function
// tool in the client's dialect (see FunctionTool), so calls and results
// round-trip without lossy action conversion. The action converters cover the
// remaining cross-dialect cases when replaying mixed histories.
//
// The canonical wire encoding of a computer ToolCall's arguments is the
// OpenAI-style call: {"call_id", "actions": [...], "pending_safety_checks": [...]}.
package computeruse

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman/pkg/provider"
)

const Name = "computer"

const (
	DialectOpenAI    = "openai"
	DialectAnthropic = "anthropic"
)

// SafetyCheck is a pending or acknowledged safety check of the OpenAI
// computer tool. Anthropic's tool has no equivalent; checks only round-trip
// between OpenAI-dialect clients and OpenAI backends.
type SafetyCheck struct {
	ID      string `json:"id,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Call is the canonical computer tool call payload.
type Call struct {
	CallID  string           `json:"call_id,omitempty"`
	Actions []map[string]any `json:"actions,omitempty"`

	PendingSafetyChecks []SafetyCheck `json:"pending_safety_checks,omitempty"`
}

// ParseCall decodes computer ToolCall arguments of either dialect into the
// canonical form. A single Anthropic action input becomes a one-element
// actions list.
func ParseCall(args string) Call {
	var call Call

	if err := json.Unmarshal([]byte(args), &call); err == nil && len(call.Actions) > 0 {
		return call
	}

	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if action, ok := FromAnthropicInput(input); ok {
		call.Actions = []map[string]any{action}
	}

	return call
}

func (c Call) Args() string {
	data, _ := json.Marshal(c)
	return string(data)
}

// AnthropicInput renders computer ToolCall arguments as an Anthropic tool_use
// input. Anthropic-dialect arguments pass through; OpenAI-dialect arguments
// degrade to the first convertible action (one action per tool_use).
func AnthropicInput(args string) map[string]any {
	var input map[string]any
	json.Unmarshal([]byte(args), &input)

	if input == nil {
		return map[string]any{}
	}

	if _, ok := input["action"].(string); ok {
		return input
	}

	for _, action := range ParseCall(args).Actions {
		if converted, ok := ToAnthropicInput(action); ok {
			return converted
		}
	}

	return input
}

// FromAnthropicInput converts an Anthropic computer tool input to an
// OpenAI-style action. ok is false when there is no counterpart
// (cursor_position, left_mouse_down/up, hold_key).
func FromAnthropicInput(input map[string]any) (map[string]any, bool) {
	command, _ := input["action"].(string)
	x, y := coordinate(input, "coordinate")

	switch command {
	case "screenshot":
		return map[string]any{"type": "screenshot"}, true

	case "left_click":
		return map[string]any{"type": "click", "button": "left", "x": x, "y": y}, true

	case "right_click":
		return map[string]any{"type": "click", "button": "right", "x": x, "y": y}, true

	case "middle_click":
		return map[string]any{"type": "click", "button": "wheel", "x": x, "y": y}, true

	case "double_click", "triple_click":
		return map[string]any{"type": "double_click", "x": x, "y": y}, true

	case "mouse_move":
		return map[string]any{"type": "move", "x": x, "y": y}, true

	case "left_click_drag":
		startX, startY := coordinate(input, "start_coordinate")
		return map[string]any{
			"type": "drag",
			"path": []map[string]any{
				{"x": startX, "y": startY},
				{"x": x, "y": y},
			},
		}, true

	case "key":
		text, _ := input["text"].(string)
		return map[string]any{"type": "keypress", "keys": strings.Split(text, "+")}, true

	case "type":
		text, _ := input["text"].(string)
		return map[string]any{"type": "type", "text": text}, true

	case "scroll":
		direction, _ := input["scroll_direction"].(string)
		amount := number(input["scroll_amount"])
		if amount <= 0 {
			amount = 1
		}

		scrollX, scrollY := 0, 0
		switch direction {
		case "up":
			scrollY = -amount * scrollPixelsPerClick
		case "down":
			scrollY = amount * scrollPixelsPerClick
		case "left":
			scrollX = -amount * scrollPixelsPerClick
		case "right":
			scrollX = amount * scrollPixelsPerClick
		}

		return map[string]any{"type": "scroll", "x": x, "y": y, "scroll_x": scrollX, "scroll_y": scrollY}, true

	case "wait":
		return map[string]any{"type": "wait"}, true
	}

	return nil, false
}

// ToAnthropicInput converts an OpenAI-style action to an Anthropic computer
// tool input. ok is false when there is no counterpart (back/forward
// buttons).
func ToAnthropicInput(action map[string]any) (map[string]any, bool) {
	kind, _ := action["type"].(string)
	x, y := number(action["x"]), number(action["y"])

	switch kind {
	case "screenshot":
		return map[string]any{"action": "screenshot"}, true

	case "click":
		button, _ := action["button"].(string)

		var command string
		switch button {
		case "", "left":
			command = "left_click"
		case "right":
			command = "right_click"
		case "wheel":
			command = "middle_click"
		default:
			return nil, false
		}

		return map[string]any{"action": command, "coordinate": []int{x, y}}, true

	case "double_click":
		return map[string]any{"action": "double_click", "coordinate": []int{x, y}}, true

	case "move":
		return map[string]any{"action": "mouse_move", "coordinate": []int{x, y}}, true

	case "drag":
		path, _ := action["path"].([]any)
		if len(path) == 0 {
			return nil, false
		}

		first, _ := path[0].(map[string]any)
		last, _ := path[len(path)-1].(map[string]any)

		return map[string]any{
			"action":           "left_click_drag",
			"start_coordinate": []int{number(first["x"]), number(first["y"])},
			"coordinate":       []int{number(last["x"]), number(last["y"])},
		}, true

	case "keypress":
		var keys []string

		switch raw := action["keys"].(type) {
		case []any:
			for _, k := range raw {
				keys = append(keys, fmt.Sprint(k))
			}
		case []string:
			keys = raw
		}

		return map[string]any{"action": "key", "text": strings.Join(keys, "+")}, true

	case "type":
		text, _ := action["text"].(string)
		return map[string]any{"action": "type", "text": text}, true

	case "scroll":
		scrollX, scrollY := number(action["scroll_x"]), number(action["scroll_y"])

		direction, amount := "down", scrollY
		switch {
		case scrollY < 0:
			direction, amount = "up", -scrollY
		case scrollY > 0:
			direction, amount = "down", scrollY
		case scrollX < 0:
			direction, amount = "left", -scrollX
		case scrollX > 0:
			direction, amount = "right", scrollX
		}

		clicks := amount / scrollPixelsPerClick
		if clicks < 1 {
			clicks = 1
		}

		return map[string]any{
			"action":           "scroll",
			"coordinate":       []int{x, y},
			"scroll_direction": direction,
			"scroll_amount":    clicks,
		}, true

	case "wait":
		return map[string]any{"action": "wait", "duration": 1}, true
	}

	return nil, false
}

// scrollPixelsPerClick approximates one mouse wheel click (Anthropic's scroll
// unit) in pixels (OpenAI's scroll unit).
const scrollPixelsPerClick = 120

func coordinate(input map[string]any, key string) (int, int) {
	raw, _ := input[key].([]any)
	if len(raw) != 2 {
		return 0, 0
	}
	return number(raw[0]), number(raw[1])
}

func number(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		f, _ := n.Float64()
		return int(f)
	}
	return 0
}

// FunctionTool renders a computer tool as a plain function tool in the same
// dialect, for backends without a native equivalent. Keeping the client's
// dialect end-to-end means calls and results need no conversion. Coordinate
// grounding still depends on the backing model's capabilities.
func FunctionTool(t provider.Tool) provider.Tool {
	display := ""
	if t.Display != nil && t.Display.Width > 0 && t.Display.Height > 0 {
		display = fmt.Sprintf(" The screen size is %dx%d pixels.", t.Display.Width, t.Display.Height)
	}

	if t.Dialect == DialectAnthropic {
		return provider.Tool{
			Name:        Name,
			Description: anthropicDescription + display,
			Parameters:  anthropicSchema(),
		}
	}

	return provider.Tool{
		Name:        Name,
		Description: openaiDescription + display,
		Parameters:  openaiSchema(),
	}
}

const anthropicDescription = `Control a computer by taking screenshots and performing mouse and keyboard actions, one action per call. ` +
	`Take a screenshot first to see the current state, act, then take another screenshot to verify the result.`

func anthropicSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"screenshot", "left_click", "right_click", "middle_click", "double_click",
					"mouse_move", "left_click_drag", "key", "type", "scroll", "wait",
				},
				"description": "The action to perform.",
			},
			"coordinate": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "[x, y] pixel coordinate for mouse actions (drag target, scroll position).",
			},
			"start_coordinate": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "[x, y] starting coordinate for left_click_drag.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to type, or the key combination to press for key (e.g. \"ctrl+s\", \"Return\").",
			},
			"scroll_direction": map[string]any{
				"type": "string",
				"enum": []string{"up", "down", "left", "right"},
			},
			"scroll_amount": map[string]any{
				"type":        "integer",
				"description": "Number of wheel clicks to scroll.",
			},
			"duration": map[string]any{
				"type":        "integer",
				"description": "Seconds to wait for the wait action.",
			},
		},
		"required": []string{"action"},
	}
}

const openaiDescription = `Control a computer by taking screenshots and performing mouse and keyboard actions. ` +
	`Take a screenshot first to see the current state, act, then take another screenshot to verify the result.`

func openaiSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"actions": map[string]any{
				"type":        "array",
				"description": "Actions to perform, in order.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{
							"type": "string",
							"enum": []string{
								"screenshot", "click", "double_click", "move", "drag",
								"keypress", "type", "scroll", "wait",
							},
						},
						"x": map[string]any{"type": "integer"},
						"y": map[string]any{"type": "integer"},
						"button": map[string]any{
							"type": "string",
							"enum": []string{"left", "right", "wheel"},
						},
						"keys": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Keys to press together for keypress (e.g. [\"CTRL\", \"S\"]).",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "Text to type.",
						},
						"path": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"x": map[string]any{"type": "integer"},
									"y": map[string]any{"type": "integer"},
								},
							},
							"description": "Points to traverse for drag.",
						},
						"scroll_x": map[string]any{"type": "integer"},
						"scroll_y": map[string]any{"type": "integer"},
					},
					"required": []string{"type"},
				},
			},
		},
		"required": []string{"actions"},
	}
}
