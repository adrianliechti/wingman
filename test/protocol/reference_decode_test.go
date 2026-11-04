package protocol_test

import (
	"maps"
	"reflect"
	"slices"
	"testing"
)

func readReference(t *testing.T, protocol string, stream bool, raw []byte) referenceResult {
	t.Helper()
	if !stream {
		return referenceJSON(t, protocol, exactObject(t, raw))
	}
	var result referenceResult
	var snapshot map[string]any
	blocks := map[int]map[string]any{}
	arguments := map[int]string{}
	chatCalls := map[int]toolCall{}
	finished, done := false, false
	for _, event := range frames(t, raw) {
		if event.Raw == "[DONE]" {
			done = true
			continue
		}
		body := exactObject(t, []byte(event.Raw))
		if body["error"] != nil || body["type"] == "error" || body["event_type"] == "error" {
			t.Fatalf("%s returned a stream error", protocol)
		}
		switch protocol {
		case "chat":
			result.text += textValue(at(body, "choices", 0, "delta", "content"))
			if reason := at(body, "choices", 0, "finish_reason"); reason == "stop" || reason == "tool_calls" {
				finished = true
				result.finish = normalizeReferenceFinish(textValue(reason))
			}
			items, _ := at(body, "choices", 0, "delta", "tool_calls").([]any)
			for _, item := range items {
				index := number(at(item, "index"))
				call := chatCalls[index]
				call.name += textValue(at(item, "function", "name"))
				call.arguments += textValue(at(item, "function", "arguments"))
				chatCalls[index] = call
			}
			result.usage(protocol, body["usage"])
		case "responses":
			switch body["type"] {
			case "response.output_text.delta":
				result.text += textValue(body["delta"])
			case "response.output_item.added":
				if at(body, "item", "type") == "function_call" {
					blocks[number(body["output_index"])], _ = body["item"].(map[string]any)
				}
			case "response.function_call_arguments.delta":
				index := number(body["output_index"])
				if blocks[index] == nil {
					t.Fatal("arguments for unopened Responses function call")
				}
				arguments[index] += textValue(body["delta"])
			case "response.completed":
				snapshot, _ = body["response"].(map[string]any)
			}
		case "anthropic":
			switch body["type"] {
			case "message_start":
				snapshot, _ = body["message"].(map[string]any)
			case "content_block_start":
				blocks[number(body["index"])], _ = body["content_block"].(map[string]any)
			case "content_block_delta":
				index := number(body["index"])
				block := blocks[index]
				if block == nil {
					t.Fatal("delta for unopened Anthropic block")
				}
				if at(body, "delta", "type") == "text_delta" {
					block["text"] = textValue(block["text"]) + textValue(at(body, "delta", "text"))
				}
				if at(body, "delta", "type") == "input_json_delta" {
					arguments[index] += textValue(at(body, "delta", "partial_json"))
				}
			case "message_delta":
				if snapshot == nil {
					t.Fatal("Anthropic delta before message_start")
				}
				delta, _ := body["delta"].(map[string]any)
				maps.Copy(snapshot, delta)
				usage, _ := snapshot["usage"].(map[string]any)
				if usage == nil {
					usage = map[string]any{}
					snapshot["usage"] = usage
				}
				update, _ := body["usage"].(map[string]any)
				maps.Copy(usage, update)
			case "message_stop":
				finished = true
			}
		case "gemini":
			part := referenceJSON(t, protocol, body)
			result.text += part.text
			result.calls = append(result.calls, part.calls...)
			result.usage(protocol, body["usageMetadata"])
			finished = finished || part.done
		case "interactions":
			switch body["event_type"] {
			case "step.start":
				blocks[number(body["index"])], _ = body["step"].(map[string]any)
			case "step.delta":
				index := number(body["index"])
				block := blocks[index]
				if block == nil {
					t.Fatal("delta for unopened interaction step")
				}
				switch at(body, "delta", "type") {
				case "text":
					block["text_deltas"] = textValue(block["text_deltas"]) + textValue(at(body, "delta", "text"))
				case "arguments_delta":
					arguments[index] += textValue(at(body, "delta", "arguments"))
				}
				result.usage(protocol, at(body, "metadata", "total_usage"))
			case "interaction.status_update":
				finished = body["status"] == "completed" || body["status"] == "requires_action"
				result.usage(protocol, body["usage"])
			case "interaction.completed":
				snapshot, _ = body["interaction"].(map[string]any)
				finished = snapshot["status"] == "completed" || snapshot["status"] == "requires_action"
				result.usage(protocol, snapshot["usage"])
			}
		}
	}
	switch protocol {
	case "chat":
		for _, index := range slices.Sorted(maps.Keys(chatCalls)) {
			call := chatCalls[index]
			result.call(t, call.name, call.arguments)
		}
		result.done = finished && done
	case "responses":
		if snapshot == nil {
			t.Fatal("no final Responses snapshot")
		}
		final := referenceJSON(t, protocol, snapshot)
		equal(t, "text deltas versus terminal snapshot", result.text, final.text)
		for _, index := range slices.Sorted(maps.Keys(blocks)) {
			block := blocks[index]
			result.call(t, textValue(block["name"]), textValue(block["arguments"])+arguments[index])
		}
		sortCalls(result.calls)
		if !reflect.DeepEqual(result.calls, final.calls) {
			t.Fatalf("tool deltas differ from terminal snapshot: got %+v, final %+v", result.calls, final.calls)
		}
		result = final
	case "anthropic":
		if snapshot == nil || !finished {
			t.Fatal("Anthropic stream did not complete")
		}
		var content []any
		for _, index := range slices.Sorted(maps.Keys(blocks)) {
			block := blocks[index]
			if partial := arguments[index]; partial != "" {
				block["input"] = exactObject(t, []byte(partial))
			}
			content = append(content, block)
		}
		snapshot["content"] = content
		result = referenceJSON(t, protocol, snapshot)
	case "gemini":
		result.done = finished
		if finished {
			result.finish = "end_turn"
			if len(result.calls) > 0 {
				result.finish = "tool_use"
			}
		}
	case "interactions":
		var steps []any
		for _, index := range slices.Sorted(maps.Keys(blocks)) {
			block := blocks[index]
			if partial := arguments[index]; partial != "" {
				block["arguments"] = exactObject(t, []byte(partial))
			}
			if text := textValue(block["text_deltas"]); text != "" {
				content, _ := block["content"].([]any)
				block["content"] = append(content, map[string]any{"type": "text", "text": text})
			}
			steps = append(steps, block)
		}
		// When provided, the final snapshot is the authoritative native result.
		if final, ok := snapshot["steps"].([]any); ok {
			steps = final
		}
		content := referenceJSON(t, protocol, map[string]any{"steps": steps})
		result.text, result.calls = content.text, content.calls
		result.done = finished
		if finished {
			result.finish = "end_turn"
			if len(result.calls) > 0 {
				result.finish = "tool_use"
			}
		}
	}
	sortCalls(result.calls)
	return result
}

func referenceJSON(t *testing.T, protocol string, body map[string]any) referenceResult {
	t.Helper()
	var result referenceResult
	var items []any
	switch protocol {
	case "chat":
		result.text = textValue(at(body, "choices", 0, "message", "content"))
		items, _ = at(body, "choices", 0, "message", "tool_calls").([]any)
		for _, item := range items {
			result.call(t, textValue(at(item, "function", "name")), at(item, "function", "arguments"))
		}
		reason := at(body, "choices", 0, "finish_reason")
		result.done = reason == "stop" || reason == "tool_calls"
		result.finish = normalizeReferenceFinish(textValue(reason))
		result.usage(protocol, body["usage"])
	case "responses":
		items, _ = body["output"].([]any)
		for _, item := range items {
			switch at(item, "type") {
			case "function_call":
				result.call(t, textValue(at(item, "name")), at(item, "arguments"))
			case "message":
				content, _ := at(item, "content").([]any)
				for _, part := range content {
					if at(part, "type") == "output_text" {
						result.text += textValue(at(part, "text"))
					}
				}
			}
		}
		result.done = body["status"] == "completed"
		if result.done {
			result.finish = "end_turn"
			if len(result.calls) > 0 {
				result.finish = "tool_use"
			}
		}
		result.usage(protocol, body["usage"])
	case "anthropic":
		items, _ = body["content"].([]any)
		for _, item := range items {
			switch at(item, "type") {
			case "text":
				result.text += textValue(at(item, "text"))
			case "tool_use":
				result.call(t, textValue(at(item, "name")), at(item, "input"))
			}
		}
		reason := body["stop_reason"]
		result.done = reason == "end_turn" || reason == "tool_use" || reason == "stop_sequence"
		result.finish = textValue(reason)
		result.usage(protocol, body["usage"])
	case "gemini":
		items, _ = at(body, "candidates", 0, "content", "parts").([]any)
		for _, item := range items {
			if at(item, "thought") != true {
				result.text += textValue(at(item, "text"))
			}
			if call := at(item, "functionCall"); call != nil {
				result.call(t, textValue(at(call, "name")), at(call, "args"))
			}
		}
		result.done = at(body, "candidates", 0, "finishReason") == "STOP"
		if result.done {
			result.finish = "end_turn"
			if len(result.calls) > 0 {
				result.finish = "tool_use"
			}
		}
		result.usage(protocol, body["usageMetadata"])
	case "interactions":
		items, _ = body["steps"].([]any)
		for _, step := range items {
			switch at(step, "type") {
			case "function_call":
				result.call(t, textValue(at(step, "name")), at(step, "arguments"))
			case "model_output":
				content, _ := at(step, "content").([]any)
				for _, part := range content {
					if at(part, "type") == "text" {
						result.text += textValue(at(part, "text"))
					}
				}
			}
		}
		result.done = body["status"] == "completed" || body["status"] == "requires_action"
		if result.done {
			result.finish = "end_turn"
			if body["status"] == "requires_action" || len(result.calls) > 0 {
				result.finish = "tool_use"
			}
		}
		result.usage(protocol, body["usage"])
	default:
		t.Fatalf("unknown reference protocol %q", protocol)
	}
	sortCalls(result.calls)
	return result
}

func normalizeReferenceFinish(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	default:
		return reason
	}
}

func (r *referenceResult) usage(protocol string, value any) {
	u, ok := value.(map[string]any)
	if !ok {
		return
	}
	var reasoning any
	switch protocol {
	case "chat":
		r.input = number(u["prompt_tokens"])
		r.output = number(u["completion_tokens"])
		r.cached = number(at(u, "prompt_tokens_details", "cached_tokens"))
		reasoning = at(u, "completion_tokens_details", "reasoning_tokens")
	case "responses":
		r.input = number(u["input_tokens"])
		r.output = number(u["output_tokens"])
		r.cached = number(at(u, "input_tokens_details", "cached_tokens"))
		reasoning = at(u, "output_tokens_details", "reasoning_tokens")
	case "anthropic":
		r.cached = number(u["cache_read_input_tokens"])
		r.input = number(u["input_tokens"]) + r.cached + number(u["cache_creation_input_tokens"])
		r.output = number(u["output_tokens"])
		reasoning = at(u, "output_tokens_details", "thinking_tokens")
	case "gemini":
		r.input = number(u["promptTokenCount"])
		r.output = number(u["candidatesTokenCount"]) + number(u["thoughtsTokenCount"])
		r.cached = number(u["cachedContentTokenCount"])
		reasoning = u["thoughtsTokenCount"]
	case "interactions":
		r.input = number(u["total_input_tokens"])
		r.output = number(u["total_output_tokens"]) + number(u["total_thought_tokens"])
		r.cached = number(u["total_cached_tokens"])
		reasoning = u["total_thought_tokens"]
	}
	r.reasoningKnown = reasoning != nil
	r.reasoning = number(reasoning)
}

func TestReferenceDecoderPreservesLargeIntegers(t *testing.T) {
	for protocol, raw := range map[string]string{
		"chat":         `{"choices":[{"message":{"tool_calls":[{"function":{"name":"record","arguments":"{\"value\":9007199254740993}"}}]},"finish_reason":"tool_calls"}]}`,
		"responses":    `{"status":"completed","output":[{"type":"function_call","name":"record","arguments":"{\"value\":9007199254740993}"}]}`,
		"anthropic":    `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"record","input":{"value":9007199254740993}}]}`,
		"gemini":       `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"functionCall":{"name":"record","args":{"value":9007199254740993}}}]}}]}`,
		"interactions": `{"status":"requires_action","steps":[{"type":"function_call","name":"record","arguments":{"value":9007199254740993}}]}`,
	} {
		t.Run(protocol, func(t *testing.T) {
			result := readReference(t, protocol, false, []byte(raw))
			if !result.done || len(result.calls) != 1 || result.calls[0].Arguments != `{"value":9007199254740993}` {
				t.Fatalf("decoder changed native result: %+v", result)
			}
		})
	}
}

// Keep this normalizer independent of production conversion and accumulators.
// These fixtures pin the different APIs' billing conventions.
func TestReferenceUsageConventions(t *testing.T) {
	for protocol, usage := range map[string]string{
		"chat":         `{"prompt_tokens":10,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}`,
		"responses":    `{"input_tokens":10,"output_tokens":7,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}`,
		"anthropic":    `{"input_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":7,"output_tokens_details":{"thinking_tokens":2}}`,
		"gemini":       `{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":2,"cachedContentTokenCount":3}`,
		"interactions": `{"total_input_tokens":10,"total_output_tokens":5,"total_thought_tokens":2,"total_cached_tokens":3}`,
	} {
		t.Run(protocol, func(t *testing.T) {
			var result referenceResult
			result.usage(protocol, exactObject(t, []byte(usage)))
			if result.input != 10 || result.output != 7 || result.reasoning != 2 || result.cached != 3 || !result.reasoningKnown {
				t.Fatalf("incorrect usage: %+v", result)
			}
		})
	}
}
