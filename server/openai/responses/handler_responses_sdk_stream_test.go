package responses

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/agent/react"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/test/harness"
)

// sdkStreamState mirrors ResponseStreamState from openai-python
// (src/openai/lib/streaming/responses/_responses.py), which backs
// client.responses.stream(). Each error is the RuntimeError or assertion the
// SDK raises, so a stream rejected here breaks Python clients mid-response.
type sdkStreamState struct {
	started   bool
	id        any
	output    map[int]map[string]any // items announced by output_item.added
	completed map[int]map[string]any // items finished by output_item.done
	positions map[sdkIdentity]int    // identity of a completed item → its index
	final     map[string]any         // response.completed payload
}

type sdkIdentity struct{ kind, id, callID string }

// Tool calls are identified by call_id, everything else by item id.
func sdkIdentityOf(item map[string]any) sdkIdentity {
	kind, _ := item["type"].(string)
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	switch kind {
	case "function_call", "custom_tool_call", "shell_call", "shell_call_output":
		id = ""
	}
	return sdkIdentity{kind, id, callID}
}

func sdkCompatibleID(previous, replacement map[string]any) bool {
	_, hasID := previous["id"].(string)
	return previous["id"] == replacement["id"] || !hasID && (previous["type"] == "function_call" || previous["type"] == "custom_tool_call")
}

func (s *sdkStreamState) item(event map[string]any) (map[string]any, error) {
	index := sdkInt(event["output_index"])
	item, ok := s.output[index]
	if !ok {
		return nil, fmt.Errorf("Received a content event for output index %d before receiving its output item", index)
	}
	return item, nil
}

func (s *sdkStreamState) handle(event map[string]any) error {
	kind, _ := event["type"].(string)
	response, _ := event["response"].(map[string]any)
	if !s.started {
		if kind != "response.created" {
			return fmt.Errorf("Expected to have received `response.created` before `%s`", kind)
		}
		s.started, s.id = true, response["id"]
		s.output, s.completed, s.positions = map[int]map[string]any{}, map[int]map[string]any{}, map[sdkIdentity]int{}
		for i, item := range sdkArray(response["output"]) {
			s.output[i] = sdkClone(item.(map[string]any))
		}
		return nil
	}
	switch kind {
	case "response.created", "response.queued", "response.in_progress", "response.completed", "response.failed", "response.incomplete":
		if response["id"] != s.id {
			return errors.New("Lifecycle response identity changed.")
		}
	}
	switch kind {
	case "response.created", "response.queued", "response.in_progress":
		replacements := map[sdkIdentity]map[string]any{}
		for _, raw := range sdkArray(response["output"]) {
			item := raw.(map[string]any)
			identity := sdkIdentityOf(item)
			if _, exists := replacements[identity]; exists {
				return errors.New("Response snapshot has conflicting output identities.")
			}
			replacements[identity] = item
		}
		for identity, replacement := range replacements {
			if index, ok := s.positions[identity]; ok {
				if !sdkCompatibleID(s.completed[index], replacement) {
					return errors.New("Response snapshot has conflicting output identities.")
				}
				s.completed[index] = sdkClone(replacement)
			}
		}
	case "response.output_item.added":
		item, ok := event["item"].(map[string]any)
		if !ok {
			return nil
		}
		index := sdkInt(event["output_index"])
		if _, exists := s.output[index]; exists {
			return fmt.Errorf("Duplicate response output index %d.", index)
		}
		s.output[index] = sdkClone(item)
	case "response.content_part.added":
		output, err := s.item(event)
		if err != nil {
			return err
		}
		if output["type"] == "message" {
			output["content"] = append(sdkArray(output["content"]), sdkClone(event["part"]))
		}
	case "response.output_text.delta", "response.output_text.done":
		output, err := s.item(event)
		if err != nil {
			return err
		}
		if output["type"] != "message" {
			return fmt.Errorf("AssertionError: %s targets a %v item", kind, output["type"])
		}
		content, index := sdkArray(output["content"]), sdkInt(event["content_index"])
		if index < 0 || index >= len(content) {
			return fmt.Errorf("IndexError: %s content_index=%d with %d parts", kind, index, len(content))
		}
		part := content[index].(map[string]any)
		if part["type"] != "output_text" {
			return fmt.Errorf("AssertionError: %s targets a %v part", kind, part["type"])
		}
		if kind == "response.output_text.delta" {
			part["text"] = part["text"].(string) + event["delta"].(string)
		}
	case "response.function_call_arguments.delta":
		output, err := s.item(event)
		if err != nil {
			return err
		}
		if output["type"] != "function_call" {
			return fmt.Errorf("AssertionError: %s targets a %v item", kind, output["type"])
		}
		arguments, _ := output["arguments"].(string)
		output["arguments"] = arguments + event["delta"].(string)
	case "response.output_item.done":
		item := event["item"].(map[string]any)
		index := sdkInt(event["output_index"])
		if added, ok := s.output[index]; ok && (sdkIdentityOf(added) != sdkIdentityOf(item) || !sdkCompatibleID(added, item)) {
			return errors.New("Completed output item identity changed.")
		}
		identity := sdkIdentityOf(item)
		if position, ok := s.positions[identity]; ok && position != index {
			return errors.New("Response snapshot has conflicting output identities.")
		}
		if previous, ok := s.completed[index]; ok {
			delete(s.positions, sdkIdentityOf(previous))
		}
		s.completed[index], s.positions[identity] = item, index
	case "response.completed":
		final := map[sdkIdentity]map[string]any{}
		for _, raw := range sdkArray(response["output"]) {
			item := raw.(map[string]any)
			final[sdkIdentityOf(item)] = item
		}
		for _, item := range s.completed {
			if replacement, ok := final[sdkIdentityOf(item)]; ok && !sdkCompatibleID(item, replacement) {
				return errors.New("Completed response output identity changed.")
			}
		}
		for index, item := range s.output {
			if _, done := s.completed[index]; done {
				continue
			}
			if replacement, ok := final[sdkIdentityOf(item)]; !ok || !sdkCompatibleID(item, replacement) {
				return errors.New("Cannot complete response with unfinished output items.")
			}
		}
		s.final = response
	}
	return nil
}

func sdkInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func sdkArray(v any) []any {
	a, _ := v.([]any)
	return a
}

func sdkClone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}

func TestSDKStreamStateRejectsKnownViolations(t *testing.T) {
	const (
		created   = `{"type":"response.created","sequence_number":0,"response":{"id":"resp","status":"in_progress","output":[]}}`
		reasoning = `{"type":"reasoning","id":"rs_1","summary":[]}`
		message   = `{"type":"message","id":"msg_1","role":"assistant","content":[]}`
	)
	added := func(index int, item string) string {
		return fmt.Sprintf(`{"type":"response.output_item.added","output_index":%d,"item":%s}`, index, item)
	}
	done := func(index int, item string) string {
		return fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item)
	}
	completed := func(output ...string) string {
		return `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[` + strings.Join(output, ",") + `]}}`
	}
	for _, tc := range []struct {
		name   string
		events []string
		want   string
	}{
		{"missing created", []string{added(0, message)}, "Expected to have received `response.created`"},
		{"duplicate output index", []string{created, added(0, reasoning), added(0, message)}, "Duplicate response output index 0."},
		{"delta before item", []string{created, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`}, "before receiving its output item"},
		{"delta without content part", []string{created, added(0, message), `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`}, "IndexError"},
		{"done changes identity", []string{created, added(0, reasoning), done(0, message)}, "Completed output item identity changed."},
		// The duplicate reasoning regression: a closed reasoning item
		// reopened under the same id at a new output index.
		{"reasoning reopened at new index", []string{created, added(0, reasoning), done(0, reasoning), added(1, message), done(1, message), added(2, reasoning), done(2, reasoning)}, "conflicting output identities"},
		{"unfinished item missing from final output", []string{created, added(0, reasoning), completed()}, "unfinished output items"},
		{"lifecycle id changes", []string{created, `{"type":"response.completed","response":{"id":"other","status":"completed","output":[]}}`}, "Lifecycle response identity changed."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state sdkStreamState
			var err error
			for _, raw := range tc.events {
				var event map[string]any
				if err := json.Unmarshal([]byte(raw), &event); err != nil {
					t.Fatal(err)
				}
				if err = state.handle(event); err != nil {
					break
				}
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func upstreamResponder(t *testing.T, bodies ...string) (*openai.Responder, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
		var request map[string]any
		if err := json.UnmarshalRead(r.Body, &request); err != nil {
			return nil, err
		}
		requests = append(requests, request)
		if len(requests) > len(bodies) {
			return nil, fmt.Errorf("unexpected upstream request %d", len(requests))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(bodies[len(requests)-1])), Request: r}, nil
	})}
	completer, err := openai.NewResponder("http://upstream.test", "gpt-6-sol", openai.WithToken("test"), openai.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	return completer, &requests
}

// checkSDKStream replays the downstream wire through the SDK state machine
// and wingman's own stricter lifecycle rules, and returns the final response.
func checkSDKStream(t *testing.T, wire string, terminal string) map[string]any {
	t.Helper()
	events, err := harness.ParseSSE(strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	var state sdkStreamState
	var added, done []map[string]any
	sequence, terminals := -1, 0
	var final map[string]any
	for _, event := range events {
		data, _ := json.Marshal(event.Data)
		if terminals != 0 {
			t.Fatalf("event after terminal response: %s", data)
		}
		if event.Event != event.Data["type"] {
			t.Fatalf("SSE event name %q does not match payload type: %s", event.Event, data)
		}
		if err := state.handle(event.Data); err != nil {
			t.Fatalf("openai-python ResponseStreamState rejects %s: %v\nwire:\n%s", data, err, wire)
		}
		// starting_after resumes by sequence number, so it must increase.
		if next := sdkInt(event.Data["sequence_number"]); next <= sequence {
			t.Fatalf("sequence_number %d after %d", next, sequence)
		} else {
			sequence = next
		}
		index, hasIndex := event.Data["output_index"].(float64)
		switch event.Event {
		case "response.output_item.added":
			if int(index) != len(added) {
				t.Fatalf("output_index %d, want %d: %s", int(index), len(added), data)
			}
			added = append(added, event.Data["item"].(map[string]any))
			done = append(done, nil)
		case "response.output_item.done":
			if done[int(index)] != nil {
				t.Fatalf("item finished twice: %s", data)
			}
			done[int(index)] = event.Data["item"].(map[string]any)
		case "response.completed", "response.incomplete", "response.failed":
			terminals++
			final = event.Data["response"].(map[string]any)
		default:
			// Item events must target the item that is open at their index.
			if itemID, ok := event.Data["item_id"]; ok && hasIndex {
				i := int(index)
				if i >= len(added) || done[i] != nil || added[i]["id"] != itemID {
					t.Fatalf("%s outside its item's lifecycle: %s", event.Event, data)
				}
			}
		}
	}
	if terminals != 1 || final["status"] != terminal {
		t.Fatalf("terminal=%v (count %d), want status %q", final["status"], terminals, terminal)
	}
	output := sdkArray(final["output"])
	if len(output) != len(added) {
		t.Fatalf("final output has %d items, stream announced %d", len(output), len(added))
	}
	ids := map[string]int{}
	for i, raw := range output {
		item := raw.(map[string]any)
		id, _ := item["id"].(string)
		if previous, exists := ids[id]; exists {
			t.Fatalf("final output repeats id %q at %d and %d", id, previous, i)
		}
		ids[id] = i
		if item["id"] != added[i]["id"] || item["type"] != added[i]["type"] {
			t.Fatalf("final output[%d]=%v/%v, stream announced %v/%v", i, item["type"], item["id"], added[i]["type"], added[i]["id"])
		}
		if done[i] == nil {
			t.Fatalf("output[%d] %v was never finished", i, item["id"])
		}
	}
	return final
}

func outputShape(output []any) []string {
	var shape []string
	for _, raw := range output {
		item := raw.(map[string]any)
		// Wingman names call items after their call_id, which is also how
		// the SDK identifies them.
		id := item["id"]
		if item["type"] == "function_call" {
			id = item["call_id"]
		}
		entry := fmt.Sprintf("%v:%v", item["type"], id)
		if c, ok := item["encrypted_content"].(string); ok {
			entry += "=" + strings.TrimPrefix(c, "@gpt-6-sol:")
		}
		if text := sdkItemText(item); text != "" {
			entry += "=" + text
		}
		if args, ok := item["arguments"].(string); ok {
			entry += "=" + args
		}
		shape = append(shape, entry)
	}
	return shape
}

func sdkItemText(item map[string]any) string {
	var text strings.Builder
	for _, raw := range sdkArray(item["content"]) {
		if part, ok := raw.(map[string]any); ok && part["type"] == "output_text" {
			text.WriteString(part["text"].(string))
		}
	}
	for _, raw := range sdkArray(item["summary"]) {
		if part, ok := raw.(map[string]any); ok {
			text.WriteString(part["text"].(string))
		}
	}
	return strings.TrimSpace(text.String())
}

const sdkStreamRequest = `{"model":"test","input":"check","stream":true,"store":false,"reasoning":{"effort":"high","summary":"auto"},"include":["reasoning.encrypted_content"],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`

// Every downstream stream must be one the OpenAI Python SDK can accumulate,
// and its final output must match the upstream's, item for item.
func TestResponsesStreamSDKCompatible(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream func() string
		terminal string
		want     []string
	}{
		// Simple: one item type per stream, terminal snapshot equals the
		// done events.
		{"text", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Message("msg_1", "final_answer", "Hel", "lo")
			return u.Finish("completed", nil)
		}, "completed", []string{"message:msg_1=Hello"}},
		{"reasoning then text", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1", "Thinking it over")
			u.Message("msg_1", "final_answer", "Done.")
			return u.Finish("completed", nil)
		}, "completed", []string{"reasoning:rs_1=enc-1=Thinking it over", "message:msg_1=Done."}},
		{"reasoning then tool call", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1", "Need data")
			u.Call("fc_1", "call_1", "lookup", `{"q":`, `"x"}`)
			return u.Finish("completed", nil)
		}, "completed", []string{"reasoning:rs_1=enc-1=Need data", `function_call:call_1={"q":"x"}`}},
		{"commentary, tool, final answer", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Message("msg_1", "commentary", "Checking.")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			u.Message("msg_2", "final_answer", "Done.")
			return u.Finish("completed", nil)
		}, "completed", []string{"message:msg_1=Checking.", "function_call:call_1={}", "message:msg_2=Done."}},

		// Hard: the terminal snapshot replays or refreshes items whose done
		// event was already forwarded, or is the only source of some state.
		{"interleaved reasoning refreshed by snapshot", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1a", "Plan")
			u.Message("msg_1", "commentary", "Looking.")
			u.Reasoning("rs_2", "enc-2a", "Pick tool")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", func(output []any) {
				harness.SetEncrypted("rs_1", "enc-1b")(output)
				harness.SetEncrypted("rs_2", "enc-2b")(output)
			})
		}, "completed", []string{"reasoning:rs_1=enc-1b=Plan", "message:msg_1=Looking.", "reasoning:rs_2=enc-2b=Pick tool", "function_call:call_1={}"}},
		{"ciphertext only in snapshot for several closed items", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "")
			u.Call("fc_1", "call_1", "lookup", `{"a":1}`)
			u.Reasoning("rs_2", "")
			u.Call("fc_2", "call_2", "lookup", `{"b":2}`)
			return u.Finish("completed", func(output []any) {
				harness.SetEncrypted("rs_1", "enc-1")(output)
				harness.SetEncrypted("rs_2", "enc-2")(output)
			})
		}, "completed", []string{"reasoning:rs_1=enc-1", `function_call:call_1={"a":1}`, "reasoning:rs_2=enc-2", `function_call:call_2={"b":2}`}},
		{"back-to-back reasoning items", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1", "First")
			u.Reasoning("rs_2", "enc-2", "Second")
			u.Message("msg_1", "final_answer", "Ok")
			return u.Finish("completed", harness.SetEncrypted("rs_1", "enc-1b"))
		}, "completed", []string{"reasoning:rs_1=enc-1b=First", "reasoning:rs_2=enc-2=Second", "message:msg_1=Ok"}},
		{"parallel tool calls after refreshed reasoning", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1", "Two lookups")
			u.Call("fc_1", "call_1", "lookup", `{"q":1}`)
			u.Call("fc_2", "call_2", "lookup", `{"q":2}`)
			return u.Finish("completed", harness.SetEncrypted("rs_1", "enc-1b"))
		}, "completed", []string{"reasoning:rs_1=enc-1b=Two lookups", `function_call:call_1={"q":1}`, `function_call:call_2={"q":2}`}},
		{"terminal summary replaces the last open item's text", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.OmitItemDone = true
			u.Reasoning("rs_1", "enc-1", "Working")
			return u.Finish("completed", func(output []any) {
				output[0].(map[string]any)["summary"] = []any{map[string]any{"type": "summary_text", "text": "Final plan"}}
			})
		}, "completed", []string{"reasoning:rs_1=enc-1=Final plan"}},
		{"no item done events, snapshot finishes everything", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.OmitItemDone = true
			u.Reasoning("rs_1", "enc-1", "Plan")
			u.Message("msg_1", "commentary", "Looking.")
			u.Reasoning("rs_2", "enc-2")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", nil)
		}, "completed", []string{"reasoning:rs_1=enc-1=Plan", "message:msg_1=Looking.", "reasoning:rs_2=enc-2", "function_call:call_1={}"}},
		{"snapshot without item events", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Output = []any{
				map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "enc-1", "status": "completed"},
				map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Hi", "annotations": []any{}}}},
			}
			return u.Finish("completed", nil)
		}, "completed", []string{"reasoning:rs_1=enc-1", "message:msg_1=Hi"}},
		// The snapshot carries a summary the done event lacked. Like a late
		// ciphertext, it refreshes the closed item instead of reopening it.
		{"summary only in snapshot after item closed", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", func(output []any) {
				output[0].(map[string]any)["summary"] = []any{map[string]any{"type": "summary_text", "text": "Late"}}
			})
		}, "completed", []string{"reasoning:rs_1=enc-1=Late", "function_call:call_1={}"}},
		{"message phase only in snapshot after item closed", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Message("msg_1", "", "Looking.")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			u.Message("msg_2", "final_answer", "Done.")
			return u.Finish("completed", func(output []any) {
				output[0].(map[string]any)["phase"] = "commentary"
			})
		}, "completed", []string{"message:msg_1=Looking.", "function_call:call_1={}", "message:msg_2=Done."}},
		{"incomplete replays closed reasoning", func() string {
			u := harness.NewResponsesStream("gpt-6-sol")
			u.Reasoning("rs_1", "enc-1", "Long answer")
			u.Message("msg_1", "final_answer", "Part")
			u.Reasoning("rs_2", "enc-2")
			return u.Finish("incomplete", harness.SetEncrypted("rs_1", "enc-1b"))
		}, "incomplete", []string{"reasoning:rs_1=enc-1b=Long answer", "message:msg_1=Part", "reasoning:rs_2=enc-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completer, _ := upstreamResponder(t, tc.upstream())
			_, wire := reviewRequestBody(t, signatures.ScopedTo("gpt-6-sol", completer), true, sdkStreamRequest)
			final := checkSDKStream(t, wire, tc.terminal)
			if got := outputShape(sdkArray(final["output"])); !slices.Equal(got, tc.want) {
				t.Fatalf("final output:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Stateless clients can replay either the terminal response or the ordered done
// items. A terminal snapshot can supply missing ciphertext or refresh an existing token.
func TestResponsesStreamOutputReplaysToUpstream(t *testing.T) {
	for _, source := range []string{"response.completed", "response.output_item.done"} {
		t.Run(source, func(t *testing.T) {
			checkResponsesReplay(t, source)
		})
	}
}

func checkResponsesReplay(t *testing.T, source string) {
	t.Helper()
	first := harness.NewResponsesStream("gpt-6-sol")
	first.Reasoning("rs_1", "", "Plan")
	first.Message("msg_1", "commentary", "Looking.")
	first.Reasoning("rs_2", "enc-2a")
	first.Call("fc_1", "call_1", "lookup", `{"q":"x"}`)
	firstBody := first.Finish("completed", func(output []any) {
		output[0].(map[string]any)["encrypted_content"] = "enc-1b"
		output[2].(map[string]any)["encrypted_content"] = "enc-2b"
	})
	second := harness.NewResponsesStream("gpt-6-sol")
	second.Message("msg_2", "final_answer", "Found it.")
	completer, requests := upstreamResponder(t, firstBody, second.Finish("completed", nil))
	scoped := signatures.ScopedTo("gpt-6-sol", completer)

	_, wire := reviewRequestBody(t, scoped, true, sdkStreamRequest)
	final := checkSDKStream(t, wire, "completed")

	var request map[string]any
	if err := json.Unmarshal([]byte(sdkStreamRequest), &request); err != nil {
		t.Fatal(err)
	}
	input := []any{map[string]any{"type": "message", "role": "user", "content": "check"}}
	output := sdkArray(final["output"])
	wantSecondToken := "enc-2b"
	if source == "response.output_item.done" {
		output = nil
		events, err := harness.ParseSSE(strings.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Event == source {
				output = append(output, event.Data["item"])
			}
		}
		wantSecondToken = "enc-2a"
	}
	input = append(input, output...)
	input = append(input, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "42"})
	request["input"] = input
	body, _ := json.Marshal(request)
	_, wire = reviewRequestBody(t, scoped, true, string(body))
	checkSDKStream(t, wire, "completed")

	if len(*requests) != 2 {
		t.Fatalf("upstream requests=%d, want 2", len(*requests))
	}
	var got []string
	ids := map[string]bool{}
	for _, raw := range sdkArray((*requests)[1]["input"]) {
		item := raw.(map[string]any)
		entry, _ := item["type"].(string)
		if entry == "" && item["role"] != nil {
			entry = "message"
		}
		if callID, ok := item["call_id"].(string); ok {
			entry += "(" + callID + ")"
		}
		if id, ok := item["id"].(string); ok && id != "" {
			if ids[id] {
				t.Errorf("upstream input repeats item id %q", id)
			}
			ids[id] = true
			entry += ":" + id
		}
		if c, ok := item["encrypted_content"].(string); ok {
			entry += "=" + c
		}
		got = append(got, entry)
	}
	want := []string{"message", "reasoning:rs_1=enc-1b", "message", "reasoning:rs_2=" + wantSecondToken, "function_call(call_1)", "function_call_output(call_1)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream replay input:\n got %q\nwant %q", got, want)
	}
}

type reactLookupTool struct{ calls int }

func (l *reactLookupTool) Tools(context.Context) ([]provider.Tool, error) {
	return []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}, nil
}

func (l *reactLookupTool) Execute(context.Context, string, map[string]any) (any, error) {
	l.calls++
	return "42", nil
}

// A configured react agent rebuilds every chunk of the model it wraps, and one
// agent response spans several upstream responses. The terminal reasoning
// copies of each must still reach the caller.
func TestResponsesThroughReactAgentKeepTerminalReasoning(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			first := harness.NewResponsesStream("gpt-6-sol")
			first.Reasoning("rs_1", "enc-1a", "Plan")
			first.Call("fc_1", "call_1", "lookup", `{}`)
			second := harness.NewResponsesStream("gpt-6-sol")
			second.Reasoning("rs_2", "")
			second.Message("msg_1", "final_answer", "Found it.")
			completer, requests := upstreamResponder(t,
				first.Finish("completed", harness.SetEncrypted("rs_1", "enc-1b")),
				second.Finish("completed", harness.SetEncrypted("rs_2", "enc-2")))
			tools := &reactLookupTool{}
			agent, err := react.New("agent", react.WithCompleter(signatures.ScopedTo("gpt-6-sol", completer)), react.WithTools(tools))
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"model":"test","input":"check","stream":%t,"store":false,"reasoning":{"effort":"high","summary":"auto"},"include":["reasoning.encrypted_content"]}`, stream)
			payload, wire := reviewRequestBody(t, agent, stream, body)
			if stream {
				checkSDKStream(t, wire, "completed")
			}
			var result struct {
				Output []any `json:"output"`
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			want := []string{"reasoning:rs_1=enc-1b=Plan", "reasoning:rs_2=enc-2", "message:msg_1=Found it."}
			if got := outputShape(result.Output); !slices.Equal(got, want) || tools.calls != 1 || len(*requests) != 2 {
				t.Fatalf("output %q (tool calls %d, upstream requests %d), want %q", got, tools.calls, len(*requests), want)
			}
		})
	}
}

func TestResponsesAnonymousMessagesAroundReasoning(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			c := reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: []provider.Content{
				provider.TextContent("Before"), provider.ReasoningContent(provider.Reasoning{Signature: "signed"}), provider.TextContent("After"),
			}}
			body := fmt.Sprintf(`{"model":"test","input":"check","stream":%t,"store":false}`, stream)
			payload, wire := reviewRequestBody(t, c, stream, body)
			var result map[string]any
			if stream {
				result = checkSDKStream(t, wire, "completed")
			} else if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			var shape []string
			ids := map[string]bool{}
			for _, raw := range sdkArray(result["output"]) {
				item := raw.(map[string]any)
				id, _ := item["id"].(string)
				if id == "" || ids[id] {
					t.Fatalf("missing or duplicate item ID %q", id)
				}
				ids[id] = true
				shape = append(shape, fmt.Sprintf("%s:%s", item["type"], sdkItemText(item)))
			}
			if want := []string{"message:Before", "reasoning:", "message:After"}; !slices.Equal(shape, want) {
				t.Fatalf("output %q, want %q", shape, want)
			}
		})
	}
}

func TestResponsesRefusalAfterClosedNativeMessage(t *testing.T) {
	c := reasoningStatusCompleter{status: provider.CompletionStatusCompleted, content: []provider.Content{
		{MessageID: "msg_native", Phase: provider.MessagePhaseCommentary, Text: "Checking"},
		provider.ReasoningContent(provider.Reasoning{Signature: "signed"}),
		{Refusal: "Cannot continue"},
	}}
	for _, stream := range []bool{true, false} {
		payload, wire := reviewRequest(t, c, stream)
		if stream {
			checkSDKStream(t, wire, "completed")
		}
		var result struct {
			Output []struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Phase string `json:"phase"`
			} `json:"output"`
		}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Output) != 3 || result.Output[0].Type != "message" || result.Output[2].Type != "message" {
			t.Fatalf("stream=%t: unexpected output %s", stream, payload)
		}
		first, last := result.Output[0], result.Output[2]
		if first.ID != "msg_native" || first.Phase != "commentary" || last.ID == "" || last.ID == first.ID || last.Phase != "" {
			t.Fatalf("stream=%t: message identities (%q, %q) and (%q, %q)", stream, first.ID, first.Phase, last.ID, last.Phase)
		}
	}
}

// A lifecycle update can list finished items in a different order from the
// stream. Only a terminal output array chooses the final membership and order.
func TestResponsesLifecycleOutput(t *testing.T) {
	u := harness.NewResponsesStream("test")
	u.Reasoning("rs_1", "", "Plan")
	u.Message("msg_1", "commentary", "Draft")
	u.Call("fc_1", "call_1", "lookup", "1")
	prefix, _, _ := strings.Cut(u.Finish("completed", nil), "event: response.completed")
	const reasoning = `{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"token"}`
	const message = `{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"Revised"}]}`
	const call = `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"2","status":"completed"}`
	const unknown = `{"id":"msg_unknown","type":"message","role":"assistant","content":[]}`
	const refresh = `{"type":"response.in_progress","response":{"id":"resp_up","output":[` + unknown + "," + call + "," + message + "," + reasoning + `]}}`
	refreshed := []string{"reasoning:rs_1=@test:token", "message:msg_1=Revised", "function_call:call_1=2"}
	for _, tc := range []struct {
		name, field string
		want        []string
	}{
		{"omitted", "", refreshed},
		{"null", `,"output":null`, refreshed},
		{"empty", `,"output":[]`, nil},
		{"subset and reordered", `,"output":[` + call + "," + message + `]`, []string{"function_call:call_1=2", "message:msg_1=Revised"}},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				terminal := `{"type":"response.completed","response":{"id":"resp_up","status":"completed"` + tc.field + `}}`
				upstream, _ := upstreamResponder(t, prefix+"data: "+refresh+"\n\ndata: "+terminal+"\n\n")
				payload, wire := reviewRequest(t, signatures.ScopedTo("test", upstream), stream)
				var result struct {
					Output []any `json:"output"`
				}
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Fatal(err)
				}
				if got := outputShape(result.Output); result.Output == nil || !slices.Equal(got, tc.want) {
					t.Fatalf("output %v, want %v", got, tc.want)
				}
				if stream {
					events, err := harness.ParseSSE(strings.NewReader(wire))
					if err != nil {
						t.Fatal(err)
					}
					var state sdkStreamState
					var done []any
					for _, event := range events {
						if err := state.handle(event.Data); err != nil {
							t.Fatal(err)
						}
						if event.Event == "response.output_item.done" {
							done = append(done, event.Data["item"])
						}
					}
					want := []string{"reasoning:rs_1=@test:token=Plan", "message:msg_1=Draft", "function_call:call_1=1"}
					if got := outputShape(done); !slices.Equal(got, want) {
						t.Fatalf("done %v, want %v", got, want)
					}
				}
			})
		}
	}
}

func TestResponsesTerminalCannotFinishAnOmittedOpenCall(t *testing.T) {
	upstream, _ := upstreamResponder(t, strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"r","output":[]}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc","delta":"{"}`,
		`data: {"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}, "\n\n")+"\n\n")
	_, wire := reviewRequest(t, upstream, true)
	if !strings.Contains(wire, "event: response.failed") || strings.Contains(wire, "event: response.output_item.done") {
		t.Fatalf("unfinished tool call received the wrong boundary:\n%s", wire)
	}
}

func TestResponsesReorderedIncompleteOutput(t *testing.T) {
	u := harness.NewResponsesStream("test")
	u.Message("msg_1", "commentary", "First")
	u.Message("msg_2", "final_answer", "Partial")
	wire := u.Finish("incomplete", func(items []any) {
		items[1].(map[string]any)["status"] = "incomplete"
		items[0], items[1] = items[1], items[0]
	})
	for _, stream := range []bool{false, true} {
		upstream, _ := upstreamResponder(t, wire)
		payload, _ := reviewRequest(t, upstream, stream)
		var result struct {
			Output []map[string]any `json:"output"`
		}
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Output) != 2 || result.Output[0]["id"] != "msg_2" || result.Output[0]["status"] != "incomplete" || result.Output[1]["id"] != "msg_1" || result.Output[1]["status"] != "completed" {
			t.Fatalf("reordered output: %s", payload)
		}
	}
}
