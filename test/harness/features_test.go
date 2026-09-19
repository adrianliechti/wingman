package harness

import (
	"net/http"
	"slices"
	"testing"
)

func TestInventory(t *testing.T) {
	exchanges := []Exchange{{
		Method: "POST", Path: "/v1/messages?beta=true", Status: 200,
		RequestHeaders: http.Header{"Anthropic-Beta": {"effort-2025-11-24,context-management-2025-06-27"}},
		Request:        `{"model":"m","thinking":{"type":"adaptive"},"tools":[{"name":"Read","input_schema":{}},{"type":"web_search_20250305","name":"web_search"}],"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"t"}]}]}`,
		Response:       "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
	}, {
		Method: "POST", Path: "/responses", Status: 400, Request: `{"model":"m","include":["reasoning.encrypted_content"],"input":[{"type":"function_call_output"}]}`, Response: `{"error":{"message":"bad"}}`,
	}}
	f := Inventory(exchanges)
	for _, want := range []string{"beta:effort-2025-11-24", "param:thinking.type=adaptive", "tool:custom", "tool:web_search_20250305", "cache_control:system", "item:user:text", "item:assistant:tool_use", "param:include=reasoning.encrypted_content", "item:function_call_output"} {
		if f.Requests[want] == 0 {
			t.Errorf("request feature %q missing: %v", want, f.Lines())
		}
	}
	for _, want := range []string{"block:text", "stop:end_turn", "usage:input_tokens", "status:200", "status:400"} {
		if f.Responses[want] == 0 {
			t.Errorf("response feature %q missing: %v", want, f.Lines())
		}
	}
	if len(f.Errors) != 1 {
		t.Errorf("errors = %v", f.Errors)
	}
	diff := Compare(Inventory(exchanges[:1]), f)
	if !slices.Contains(diff, "request item:function_call_output: actual only") {
		t.Errorf("compare = %v", diff)
	}
}
