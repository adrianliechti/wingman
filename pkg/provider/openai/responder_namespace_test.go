package openai

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// TestResponderTools_NamespaceCustomChild verifies a Codex-style namespace with
// an empty description and a grammar-based custom child (e.g. code mode's
// `exec`) serializes with a non-empty description and keeps the custom tool.
func TestResponderTools_NamespaceCustomChild(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	options := &provider.CompleteOptions{
		Tools: []provider.Tool{
			{
				Name: "functions",
				Tools: []provider.Tool{
					{
						Kind:        provider.ToolKindCustom,
						Name:        "exec",
						Description: "Run JavaScript code.",
						Format: &provider.ToolFormat{
							Type:       "grammar",
							Syntax:     "lark",
							Definition: "start: SOURCE\nSOURCE: /[\\s\\S]+/",
						},
					},
					{
						Name:        "wait",
						Description: "Waits on a yielded exec cell.",
						Parameters:  map[string]any{"type": "object"},
					},
				},
			},
		},
	}

	body := responsesRequestBody(t, responder, []provider.Message{provider.UserMessage("hi")}, options)

	tools := requestTools(t, body)
	if len(tools) != 1 || tools[0]["type"] != "namespace" {
		t.Fatalf("tools = %+v, want one namespace tool", tools)
	}

	ns := tools[0]
	if desc, _ := ns["description"].(string); desc == "" {
		t.Fatalf("namespace description is empty: %+v", ns)
	}

	children := ns["tools"].([]any)
	if len(children) != 2 {
		t.Fatalf("children = %+v, want exec and wait", children)
	}

	byName := map[string]map[string]any{}
	for _, child := range children {
		m := child.(map[string]any)
		byName[m["name"].(string)] = m
	}

	exec := byName["exec"]
	if exec == nil || exec["type"] != "custom" {
		t.Fatalf("exec child: %+v", exec)
	}
	format := exec["format"].(map[string]any)
	if format["type"] != "grammar" || format["syntax"] != "lark" || format["definition"] != "start: SOURCE\nSOURCE: /[\\s\\S]+/" {
		t.Fatalf("exec format: %+v", format)
	}

	wait := byName["wait"]
	if wait == nil || wait["type"] != "function" {
		t.Fatalf("wait child: %+v", wait)
	}
}

// TestResponderInput_CustomToolCallKeepsNamespace verifies a namespaced custom
// tool call replays with its namespace field intact.
func TestResponderInput_CustomToolCallKeepsNamespace(t *testing.T) {
	responder, _ := NewResponder("https://api.openai.com/v1/", "gpt-test")

	messages := []provider.Message{
		{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{
				provider.ToolCallContent(provider.ToolCall{
					ID:        "call_q1",
					Kind:      provider.ToolKindCustom,
					Name:      "run_query",
					Namespace: "mcp__db__",
					Arguments: "SELECT 1",
				}),
			},
		},
	}

	body := responsesRequestBody(t, responder, messages, &provider.CompleteOptions{})

	items, _ := body["input"].([]any)
	if len(items) != 1 {
		t.Fatalf("input = %+v, want one custom_tool_call item", items)
	}

	item := items[0].(map[string]any)
	if item["type"] != "custom_tool_call" || item["namespace"] != "mcp__db__" || item["input"] != "SELECT 1" {
		t.Fatalf("custom_tool_call item: %+v", item)
	}
}

func TestResponderAdditionalToolsPosition(t *testing.T) {
	initial := provider.Tool{Name: "before"}
	added := provider.Tool{Name: "later"}
	addition := &provider.AdditionalTools{ID: "at_1", Tools: []provider.Tool{added}}
	added.InputScoped = true
	options := &provider.CompleteOptions{Tools: []provider.Tool{initial, added}}
	messages := []provider.Message{provider.UserMessage("before"), {Content: []provider.Content{{AdditionalTools: addition}}}, provider.UserMessage("after")}

	for _, tc := range []struct {
		endpoint, model string
		positional      bool
	}{
		{"https://api.openai.com/v1", "gpt-6.2-astra", true},
		{"https://api.openai.com/v1", "gpt-6.2-sol", true},
		{"https://api.openai.com/v1", "gpt-4o", false},
		{"https://example.test/v1", "gpt-6.2-sol", false},
	} {
		t.Run(tc.endpoint+"/"+tc.model, func(t *testing.T) {
			responder, err := NewResponder(tc.endpoint, tc.model)
			if err != nil {
				t.Fatal(err)
			}
			body := responsesRequestBody(t, responder, messages, options)
			tools := requestTools(t, body)
			input := body["input"].([]any)
			if !tc.positional {
				if len(input) != 2 || len(tools) != 2 || tools[1]["name"] != "later" {
					t.Fatalf("request-wide tool availability changed: %+v", body)
				}
				return
			}
			if len(input) != 3 || len(tools) != 1 || tools[0]["name"] != "before" {
				t.Fatalf("initial tool list or input order changed: %+v", body)
			}
			item := input[1].(map[string]any)
			more := requestTools(t, item)
			if item["type"] != "additional_tools" || item["role"] != "developer" || item["id"] != "at_1" || len(more) != 1 || more[0]["name"] != "later" {
				t.Fatalf("tool declaration changed: %+v", item)
			}
		})
	}
}
