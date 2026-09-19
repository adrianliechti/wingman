package harness

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Features summarizes what a recorded conversation used on the wire, so a
// CLI run can be inventoried for request parameters, tool types, betas and
// the item or block types the API answered with. Counts are per exchange.
type Features struct {
	Requests  map[string]int
	Responses map[string]int
	Errors    []string
}

// Inventory scans recorded exchanges of the Messages or Responses API.
func Inventory(exchanges []Exchange) Features {
	f := Features{Requests: map[string]int{}, Responses: map[string]int{}}

	for _, e := range exchanges {
		path := e.Path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		f.Requests["path:"+e.Method+" "+path]++
		f.Responses[fmt.Sprintf("status:%d", e.Status)]++
		if e.Status >= 400 {
			f.Errors = append(f.Errors, fmt.Sprintf("%s %s -> %d: %.300s", e.Method, e.Path, e.Status, e.Response))
		}

		for _, header := range []string{"Anthropic-Beta", "Openai-Beta"} {
			for _, value := range e.RequestHeaders.Values(header) {
				for beta := range strings.SplitSeq(value, ",") {
					if beta = strings.TrimSpace(beta); beta != "" {
						f.Requests["beta:"+beta]++
					}
				}
			}
		}

		var request map[string]any
		if json.Unmarshal([]byte(e.Request), &request) == nil {
			inventoryRequest(f.Requests, request)
		}
		inventoryResponse(f.Responses, e.Response)
	}

	return f
}

func inventoryRequest(out map[string]int, request map[string]any) {
	for key, value := range request {
		out["key:"+key]++
		switch key {
		case "thinking", "output_config", "reasoning", "text", "context_management", "tool_choice", "truncation", "store", "parallel_tool_calls", "service_tier", "speed", "temperature", "max_tokens", "max_output_tokens":
			for _, param := range scalarParams(key, value) {
				out["param:"+param]++
			}
		case "include":
			if list, ok := value.([]any); ok {
				for _, item := range list {
					out[fmt.Sprintf("param:include=%v", item)]++
				}
			}
		case "system":
			if blocks, ok := value.([]any); ok {
				for _, raw := range blocks {
					if block, ok := raw.(map[string]any); ok && block["cache_control"] != nil {
						out["cache_control:system"]++
					}
				}
			}
		}
	}

	if tools, ok := request["tools"].([]any); ok {
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			kind, _ := tool["type"].(string)
			if kind == "" {
				kind = "custom"
			}
			out["tool:"+kind]++
			for _, flag := range []string{"defer_loading", "strict", "eager_input_streaming", "cache_control", "input_examples", "allowed_callers", "format"} {
				if tool[flag] != nil {
					out["tool_flag:"+flag]++
				}
			}
		}
	}

	for _, key := range []string{"messages", "input"} {
		items, ok := request[key].([]any)
		if !ok {
			continue
		}
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			role, _ := item["role"].(string)
			kind, _ := item["type"].(string)
			if role == "" {
				out["item:"+kind]++
				continue
			}
			switch content := item["content"].(type) {
			case string:
				out["item:"+role+":text"]++
			case []any:
				for _, rawBlock := range content {
					block, _ := rawBlock.(map[string]any)
					blockKind, _ := block["type"].(string)
					out["item:"+role+":"+blockKind]++
					if block["cache_control"] != nil {
						out["cache_control:"+role+":"+blockKind]++
					}
				}
			}
		}
	}
}

func scalarParams(prefix string, value any) []string {
	switch v := value.(type) {
	case map[string]any:
		var params []string
		for key, nested := range v {
			params = append(params, scalarParams(prefix+"."+key, nested)...)
		}
		return params
	case []any:
		return []string{prefix + "=[...]"}
	default:
		return []string{fmt.Sprintf("%s=%v", prefix, v)}
	}
}

func inventoryResponse(out map[string]int, body string) {
	if !strings.Contains(body, "data: ") {
		var response map[string]any
		if json.Unmarshal([]byte(body), &response) == nil {
			inventoryResponseObject(out, response)
		}
		return
	}
	events, err := ParseSSE(strings.NewReader(body))
	if err != nil {
		return
	}
	for _, event := range events {
		if event.Data == nil {
			continue
		}
		switch eventName(event) {
		case "message_start":
			if message, ok := event.Data["message"].(map[string]any); ok {
				inventoryResponseObject(out, message)
			}
		case "content_block_start":
			if block, ok := event.Data["content_block"].(map[string]any); ok {
				out[fmt.Sprintf("block:%v", block["type"])]++
			}
		case "message_delta":
			if delta, ok := event.Data["delta"].(map[string]any); ok && delta["stop_reason"] != nil {
				out[fmt.Sprintf("stop:%v", delta["stop_reason"])]++
			}
		case "response.output_item.added":
			if item, ok := event.Data["item"].(map[string]any); ok {
				out[fmt.Sprintf("item:%v", item["type"])]++
			}
		case "response.completed", "response.incomplete", "response.failed":
			if response, ok := event.Data["response"].(map[string]any); ok {
				inventoryResponseObject(out, response)
			}
		case "error":
			out["event:error"]++
		}
	}
}

func inventoryResponseObject(out map[string]int, object map[string]any) {
	if status, ok := object["status"].(string); ok {
		out["response_status:"+status]++
	}
	if reason, ok := object["stop_reason"].(string); ok {
		out["stop:"+reason]++
	}
	if usage, ok := object["usage"].(map[string]any); ok {
		for key := range usage {
			out["usage:"+key]++
		}
	}
	if content, ok := object["content"].([]any); ok {
		for _, raw := range content {
			if block, ok := raw.(map[string]any); ok {
				out[fmt.Sprintf("block:%v", block["type"])]++
			}
		}
	}
	if output, ok := object["output"].([]any); ok {
		for _, raw := range output {
			if item, ok := raw.(map[string]any); ok {
				out[fmt.Sprintf("item:%v", item["type"])]++
			}
		}
	}
}

// Lines renders the inventory sorted, one feature per line.
func (f Features) Lines() []string {
	var lines []string
	for key, count := range f.Requests {
		lines = append(lines, fmt.Sprintf("request %s x%d", key, count))
	}
	for key, count := range f.Responses {
		lines = append(lines, fmt.Sprintf("response %s x%d", key, count))
	}
	sort.Strings(lines)
	return append(lines, f.Errors...)
}

// Compare reports features one side used and the other did not, ignoring
// counts. Both sides usually receive the same client requests; differences
// show what a gateway dropped, added or answered differently.
func Compare(reference, actual Features) []string {
	var lines []string
	for label, pair := range map[string][2]map[string]int{"request": {reference.Requests, actual.Requests}, "response": {reference.Responses, actual.Responses}} {
		for key := range pair[0] {
			if pair[1][key] == 0 {
				lines = append(lines, fmt.Sprintf("%s %s: reference only", label, key))
			}
		}
		for key := range pair[1] {
			if pair[0][key] == 0 {
				lines = append(lines, fmt.Sprintf("%s %s: actual only", label, key))
			}
		}
	}
	sort.Strings(lines)
	return append(lines, actual.Errors...)
}
