package harness

import (
	"encoding/json/v2"
	"fmt"
	"strings"
)

// ResponsesStream writes an upstream OpenAI Responses SSE stream with the
// event order the live API uses for each output item type, for tests that
// drive a provider through a fake transport.
type ResponsesStream struct {
	b     strings.Builder
	seq   int
	index int
	model string

	// Output collects the finished items; Finish sends it as the terminal
	// snapshot and tests may preset it to send a snapshot without item events.
	Output []any

	// Some compatible servers only send deltas and the terminal snapshot.
	OmitItemDone bool
}

func NewResponsesStream(model string) *ResponsesStream {
	u := &ResponsesStream{model: model}
	for _, kind := range []string{"response.created", "response.in_progress"} {
		u.emit(kind, map[string]any{"response": map[string]any{"id": "resp_up", "model": model, "status": "in_progress", "output": []any{}}})
	}
	return u
}

func (u *ResponsesStream) emit(kind string, event map[string]any) {
	event["type"], event["sequence_number"] = kind, u.seq
	u.seq++
	data, _ := json.Marshal(event, json.Deterministic(true))
	fmt.Fprintf(&u.b, "event: %s\ndata: %s\n\n", kind, data)
}

func (u *ResponsesStream) itemDone(index int, item map[string]any) {
	if !u.OmitItemDone {
		u.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
	u.Output = append(u.Output, item)
}

// Reasoning streams a reasoning item. encrypted is set on the done item only,
// as with include=["reasoning.encrypted_content"].
func (u *ResponsesStream) Reasoning(id, encrypted string, summary ...string) {
	index := u.index
	u.index++
	u.emit("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"type": "reasoning", "id": id, "summary": []any{}}})
	parts := []any{}
	for i, text := range summary {
		at := func(fields map[string]any) map[string]any {
			fields["output_index"], fields["item_id"], fields["summary_index"] = index, id, i
			return fields
		}
		u.emit("response.reasoning_summary_part.added", at(map[string]any{"part": map[string]any{"type": "summary_text", "text": ""}}))
		for chunk := range strings.SplitAfterSeq(text, " ") {
			u.emit("response.reasoning_summary_text.delta", at(map[string]any{"delta": chunk}))
		}
		u.emit("response.reasoning_summary_text.done", at(map[string]any{"text": text}))
		part := map[string]any{"type": "summary_text", "text": text}
		u.emit("response.reasoning_summary_part.done", at(map[string]any{"part": part}))
		parts = append(parts, part)
	}
	item := map[string]any{"type": "reasoning", "id": id, "summary": parts, "status": "completed"}
	if encrypted != "" {
		item["encrypted_content"] = encrypted
	}
	u.itemDone(index, item)
}

func (u *ResponsesStream) Message(id, phase string, chunks ...string) {
	index := u.index
	u.index++
	item := map[string]any{"type": "message", "id": id, "role": "assistant", "status": "in_progress", "content": []any{}}
	if phase != "" {
		item["phase"] = phase
	}
	u.emit("response.output_item.added", map[string]any{"output_index": index, "item": clone(item)})
	u.emit("response.content_part.added", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	for _, chunk := range chunks {
		u.emit("response.output_text.delta", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "delta": chunk, "logprobs": []any{}})
	}
	text := strings.Join(chunks, "")
	u.emit("response.output_text.done", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "text": text, "logprobs": []any{}})
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
	u.emit("response.content_part.done", map[string]any{"output_index": index, "item_id": id, "content_index": 0, "part": part})
	item["status"], item["content"] = "completed", []any{part}
	u.itemDone(index, item)
}

func (u *ResponsesStream) Call(id, callID, name string, chunks ...string) {
	index := u.index
	u.index++
	item := map[string]any{"type": "function_call", "id": id, "call_id": callID, "name": name, "arguments": "", "status": "in_progress"}
	u.emit("response.output_item.added", map[string]any{"output_index": index, "item": clone(item)})
	for _, chunk := range chunks {
		u.emit("response.function_call_arguments.delta", map[string]any{"output_index": index, "item_id": id, "delta": chunk})
	}
	arguments := strings.Join(chunks, "")
	u.emit("response.function_call_arguments.done", map[string]any{"output_index": index, "item_id": id, "arguments": arguments})
	item["status"], item["arguments"] = "completed", arguments
	u.itemDone(index, item)
}

// Finish sends the terminal snapshot and returns the whole stream. edit may
// change the snapshot, e.g. to refresh a reasoning ciphertext the way the live
// API sometimes does.
func (u *ResponsesStream) Finish(status string, edit func(output []any)) string {
	output := clone(u.Output)
	if output == nil {
		output = []any{}
	}
	if edit != nil {
		edit(output)
	}
	response := map[string]any{"id": "resp_up", "model": u.model, "status": status, "output": output,
		"usage": map[string]any{"input_tokens": 12, "output_tokens": 7, "total_tokens": 19}}
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	u.emit("response."+status, map[string]any{"response": response})
	return u.b.String()
}

// SetEncrypted returns a Finish edit that replaces one item's ciphertext.
func SetEncrypted(id, ciphertext string) func([]any) {
	return func(output []any) {
		for _, raw := range output {
			if item := raw.(map[string]any); item["id"] == id {
				item["encrypted_content"] = ciphertext
			}
		}
	}
}

func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
