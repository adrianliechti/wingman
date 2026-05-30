package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

// namespaceCompleter captures the provider.Tool list and prior tool-call
// messages, then yields a single namespaced function_call response.
type namespaceCompleter struct {
	t *testing.T

	gotTools    []provider.Tool
	gotMessages []provider.Message

	reply provider.ToolCall
}

func (c *namespaceCompleter) Complete(_ context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options != nil {
			c.gotTools = options.Tools
		}
		c.gotMessages = messages

		yield(&provider.Completion{
			ID:     "resp_ns",
			Model:  "namespace-model",
			Status: provider.CompletionStatusCompleted,
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{
					provider.ToolCallContent(c.reply),
				},
			},
		}, nil)
	}
}

// TestNamespaceToolFlattensToFunctionTools verifies that a Codex-style namespace
// tool (e.g. "mcp__weather__") with inner function tools is expanded into
// individual provider.Tool entries, each tagged with the namespace label so the
// downstream provider can re-group or pass them through.
func TestNamespaceToolFlattensToFunctionTools(t *testing.T) {
	const modelID = "namespace-model"

	completer := &namespaceCompleter{
		t: t,
		reply: provider.ToolCall{
			ID:        "call_ns_1",
			Name:      "get_forecast",
			Namespace: "mcp__weather__",
			Arguments: `{"city":"Zurich"}`,
		},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"stream": false,
		"tools": [
			{
				"type": "namespace",
				"name": "mcp__weather__",
				"description": "Weather MCP tools.",
				"tools": [
					{
						"type": "function",
						"name": "get_forecast",
						"description": "Get the forecast.",
						"parameters": {"type":"object","properties":{"city":{"type":"string"}}}
					},
					{
						"type": "function",
						"name": "get_history",
						"description": "Get history.",
						"parameters": {"type":"object","properties":{"city":{"type":"string"}}}
					}
				]
			}
		],
		"input": "What's the weather in Zurich?"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(completer.gotTools) != 1 {
		t.Fatalf("expected 1 namespace tool, got %d (%+v)", len(completer.gotTools), completer.gotTools)
	}

	ns := completer.gotTools[0]
	if ns.Name != "mcp__weather__" {
		t.Fatalf("namespace name = %q, want mcp__weather__", ns.Name)
	}
	if ns.Description != "Weather MCP tools." {
		t.Fatalf("namespace description = %q, want %q", ns.Description, "Weather MCP tools.")
	}
	if len(ns.Tools) != 2 {
		t.Fatalf("expected 2 child tools, got %d (%+v)", len(ns.Tools), ns.Tools)
	}

	byName := map[string]provider.Tool{}
	for _, child := range ns.Tools {
		byName[child.Name] = child
	}
	if d := byName["get_forecast"].Description; d != "Get the forecast." {
		t.Errorf("inner forecast description = %q", d)
	}
	if d := byName["get_history"].Description; d != "Get history." {
		t.Errorf("inner history description = %q", d)
	}

	var resp struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, rec.Body.String())
	}
	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item, got %+v", resp.Output)
	}
	item := resp.Output[0]
	if item["type"] != "function_call" {
		t.Fatalf("expected function_call output, got %+v", item)
	}
	if item["namespace"] != "mcp__weather__" {
		t.Fatalf("expected namespace field echoed back, got %+v", item)
	}
	if item["name"] != "get_forecast" || item["call_id"] != "call_ns_1" {
		t.Fatalf("unexpected function_call fields: %+v", item)
	}
}

// TestNamespaceToolRoundTripsPriorCall verifies that a prior function_call
// input item with a namespace field is passed back to the provider with the
// namespace preserved (so multi-turn dispatch finds the right tool in Codex's
// registry).
func TestNamespaceToolRoundTripsPriorCall(t *testing.T) {
	const modelID = "namespace-rt-model"

	completer := &namespaceCompleter{
		t: t,
		reply: provider.ToolCall{
			ID:   "call_ns_2",
			Name: "no_op",
		},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"stream": false,
		"tools": [
			{
				"type": "namespace",
				"name": "mcp__weather__",
				"description": "Weather MCP tools.",
				"tools": [
					{"type": "function", "name": "get_forecast", "parameters": {"type":"object"}}
				]
			}
		],
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "fetch"}]},
			{
				"type": "function_call",
				"id": "fc_prior",
				"call_id": "call_prior_ns",
				"name": "get_forecast",
				"namespace": "mcp__weather__",
				"arguments": "{\"city\":\"Bern\"}",
				"status": "completed"
			},
			{
				"type": "function_call_output",
				"call_id": "call_prior_ns",
				"output": "sunny"
			},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "now what?"}]}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sawPriorCall bool
	for _, msg := range completer.gotMessages {
		for _, content := range msg.Content {
			if content.ToolCall == nil || content.ToolCall.ID != "call_prior_ns" {
				continue
			}
			sawPriorCall = true
			if content.ToolCall.Namespace != "mcp__weather__" {
				t.Fatalf("prior call namespace lost: %+v", *content.ToolCall)
			}
			if !strings.Contains(content.ToolCall.Arguments, "Bern") {
				t.Fatalf("prior call arguments lost: %+v", *content.ToolCall)
			}
		}
	}
	if !sawPriorCall {
		t.Fatalf("prior namespaced function_call did not reach provider: %+v", completer.gotMessages)
	}
}
