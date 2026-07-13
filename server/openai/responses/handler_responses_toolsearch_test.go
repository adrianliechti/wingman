package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

// toolSearchCompleter captures what the provider sees and replies with one
// tool_search_call so we can verify the output round-trip.
type toolSearchCompleter struct {
	t *testing.T

	gotTools    []provider.Tool
	gotMessages []provider.Message

	reply provider.ToolCall
}

func (c *toolSearchCompleter) Complete(_ context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		if options != nil {
			c.gotTools = options.Tools
		}
		c.gotMessages = messages

		yield(&provider.Completion{
			ID:     "resp_ts",
			Model:  "tool-search-model",
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

// TestToolSearchRequestAndResponse verifies:
//   - A `tool_search` tool definition reaches the provider as a Tool with
//     Kind=ToolSearch and the execution/parameters preserved.
//   - A function tool tagged `defer_loading: true` arrives with Deferred set.
//   - When the model emits a tool_search_call, wingman serializes it back to
//     codex as `{"type":"tool_search_call", ...}` with arguments+execution intact.
func TestToolSearchRequestAndResponse(t *testing.T) {
	const modelID = "tool-search-model"

	completer := &toolSearchCompleter{
		t: t,
		reply: provider.ToolCall{
			ID:        "call_ts_1",
			Name:      "tool_search",
			Kind:      provider.ToolKindToolSearch,
			Execution: "client",
			Arguments: `{"paths":["mcp__weather__"]}`,
		},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"stream": false,
		"tools": [
			{
				"type": "tool_search",
				"execution": "client",
				"description": "Find project-specific tools.",
				"parameters": {"type":"object","properties":{"goal":{"type":"string"}},"required":["goal"]}
			},
			{
				"type": "function",
				"name": "rare_helper",
				"description": "Rarely needed.",
				"parameters": {"type":"object"},
				"defer_loading": true
			}
		],
		"input": "find me a tool"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sawToolSearch, sawDeferredFn bool
	for _, tool := range completer.gotTools {
		if tool.Kind == provider.ToolKindToolSearch {
			sawToolSearch = true
			if tool.Execution != "client" {
				t.Fatalf("tool_search execution lost: %q", tool.Execution)
			}
			if tool.Parameters == nil {
				t.Fatalf("tool_search parameters lost")
			}
		}
		if tool.Name == "rare_helper" {
			sawDeferredFn = true
			if tool.Deferred == nil || !*tool.Deferred {
				t.Fatalf("defer_loading not passed through on function tool: %+v", tool)
			}
		}
	}
	if !sawToolSearch {
		t.Fatalf("tool_search tool not forwarded to provider: %+v", completer.gotTools)
	}
	if !sawDeferredFn {
		t.Fatalf("deferred function tool not forwarded to provider: %+v", completer.gotTools)
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
	if item["type"] != "tool_search_call" {
		t.Fatalf("expected tool_search_call output, got %+v", item)
	}
	if item["call_id"] != "call_ts_1" {
		t.Fatalf("call_id mismatch: %+v", item)
	}
	if item["execution"] != "client" {
		t.Fatalf("execution mismatch: %+v", item)
	}
	if args, ok := item["arguments"].(map[string]any); !ok {
		t.Fatalf("arguments not a JSON object: %+v", item)
	} else if paths, ok := args["paths"].([]any); !ok || len(paths) != 1 || paths[0] != "mcp__weather__" {
		t.Fatalf("arguments.paths mismatch: %+v", args)
	}
}

// TestAdditionalToolsInputReachesProvider covers Codex Responses Lite, which
// puts its tool definitions in a leading additional_tools input item and omits
// the request-level tools field.
func TestAdditionalToolsInputReachesProvider(t *testing.T) {
	const modelID = "additional-tools-model"

	completer := &toolSearchCompleter{
		t:     t,
		reply: provider.ToolCall{ID: "call_customer", Name: "get_customer", Arguments: `{"customer_id":"cus_123"}`},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"stream": false,
		"input": [
			{
				"type": "additional_tools",
				"role": "developer",
				"tools": [
					{
						"type": "function",
						"name": "get_customer",
						"description": "Look up a customer by ID.",
						"parameters": {
							"type": "object",
							"properties": {"customer_id": {"type": "string"}},
							"required": ["customer_id"],
							"additionalProperties": false
						}
					},
					{"type": "web_search"}
				]
			},
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "Look up cus_123."}]
			}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(completer.gotTools) != 1 || completer.gotTools[0].Name != "get_customer" {
		t.Fatalf("additional_tools did not reach provider: %+v", completer.gotTools)
	}
	if len(completer.gotMessages) != 1 || completer.gotMessages[0].Role != provider.MessageRoleUser {
		t.Fatalf("conversation input changed unexpectedly: %+v", completer.gotMessages)
	}
}

// TestToolSearchOutputRoundTripsToProvider verifies that a prior
// tool_search_output input item — the result codex returned to the model on a
// previous turn — passes through to the provider as a ToolResult with
// Kind=ToolSearch, the execution label preserved, and the tools[] payload
// kept verbatim so the next request can replay it to OpenAI.
func TestToolSearchOutputRoundTripsToProvider(t *testing.T) {
	const modelID = "tool-search-rt-model"

	completer := &toolSearchCompleter{
		t:     t,
		reply: provider.ToolCall{ID: "call_done", Name: "noop"},
	}

	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(modelID, completer)

	body := []byte(`{
		"model": "` + modelID + `",
		"stream": false,
		"tools": [{"type": "tool_search", "execution": "client"}],
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "search"}]},
			{
				"type": "tool_search_call",
				"id": "tsc_prior",
				"call_id": "call_prior_ts",
				"status": "completed",
				"execution": "client",
				"arguments": {"paths":["mcp__weather__"]}
			},
			{
				"type": "tool_search_output",
				"call_id": "call_prior_ts",
				"status": "completed",
				"execution": "client",
				"tools": [
					{"type":"function","name":"get_forecast","parameters":{"type":"object"}}
				]
			},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "use it"}]}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(cfg).handleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sawPriorCall, sawPriorOutput bool
	for _, msg := range completer.gotMessages {
		for _, content := range msg.Content {
			if content.ToolCall != nil && content.ToolCall.ID == "call_prior_ts" {
				sawPriorCall = true
				if content.ToolCall.Kind != provider.ToolKindToolSearch {
					t.Fatalf("prior tool_search_call kind lost: %+v", *content.ToolCall)
				}
				if content.ToolCall.Execution != "client" {
					t.Fatalf("prior tool_search_call execution lost: %+v", *content.ToolCall)
				}
			}
			if content.ToolResult != nil && content.ToolResult.ID == "call_prior_ts" {
				sawPriorOutput = true
				if content.ToolResult.Kind != provider.ToolKindToolSearch {
					t.Fatalf("prior tool_search_output kind lost: %+v", *content.ToolResult)
				}
				if content.ToolResult.Execution != "client" {
					t.Fatalf("prior tool_search_output execution lost: %+v", *content.ToolResult)
				}
				if len(content.ToolResult.Payload) == 0 {
					t.Fatalf("prior tool_search_output payload empty: %+v", *content.ToolResult)
				}
				var parsed []any
				if err := json.Unmarshal(content.ToolResult.Payload, &parsed); err != nil {
					t.Fatalf("payload not valid JSON: %v: %s", err, string(content.ToolResult.Payload))
				}
				if len(parsed) != 1 {
					t.Fatalf("expected 1 tool in payload, got %+v", parsed)
				}
			}
		}
	}
	if !sawPriorCall {
		t.Fatalf("prior tool_search_call did not reach provider: %+v", completer.gotMessages)
	}
	if !sawPriorOutput {
		t.Fatalf("prior tool_search_output did not reach provider: %+v", completer.gotMessages)
	}
}
