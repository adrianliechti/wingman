package responses

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// These tests pin Responses API properties observed in Codex TUI HAR captures
// and the corresponding reference event shape.

// Reference reasoning item in response.completed.output[] carries
// {id, type, summary, encrypted_content, optionally content} — but NOT
// `status`. Status only appears on streaming output_item.added/done.
func TestReasoningOutputInResponseCompletedShape(t *testing.T) {
	outputs := responseOutputs(&provider.Message{
		Content: []provider.Content{
			provider.ReasoningContent(provider.Reasoning{
				ID:        "rs_1",
				Text:      "let me think",
				Signature: "ENC_1",
			}),
		},
	}, "msg_1", "completed", responseOutputOptions{IncludeReasoning: true})

	if len(outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(outputs))
	}

	data, err := json.Marshal(outputs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := got["status"]; ok {
		t.Fatalf("reasoning in response.completed.output[] must NOT carry `status`, got %s", data)
	}

	content, ok := got["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected content array with one entry, got %v (raw: %s)", got["content"], data)
	}
	part := content[0].(map[string]any)
	if part["type"] != "reasoning_text" || part["text"] != "let me think" {
		t.Fatalf("expected reasoning_text part, got %v", part)
	}

	if got["encrypted_content"] != "ENC_1" {
		t.Fatalf("expected encrypted_content to survive, got %v", got["encrypted_content"])
	}
}

// reasoning.context is part of the OpenAI Responses reference response
// shape and defaults to "current_turn" when the client omits it.
func TestResponseDefaultsFillsReasoningContext(t *testing.T) {
	cases := []struct {
		name string
		req  ResponsesRequest
		want string
	}{
		{
			name: "no reasoning provided",
			req:  ResponsesRequest{},
			want: "current_turn",
		},
		{
			name: "reasoning provided without context",
			req: ResponsesRequest{
				Reasoning: &ReasoningConfig{},
			},
			want: "current_turn",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &Response{}
			responseDefaults(resp, tc.req)
			if resp.Reasoning == nil || resp.Reasoning.Context == nil {
				t.Fatalf("expected reasoning.context to be set, got %+v", resp.Reasoning)
			}
			if *resp.Reasoning.Context != tc.want {
				t.Fatalf("expected context=%q, got %q", tc.want, *resp.Reasoning.Context)
			}
		})
	}
}

// When the client explicitly sets a context value, the response must
// echo it (matches OpenAI's pass-through behavior).
func TestResponseDefaultsEchoesReasoningContextFromRequest(t *testing.T) {
	ctx := "ephemeral"
	resp := &Response{}
	responseDefaults(resp, ResponsesRequest{
		Reasoning: &ReasoningConfig{Context: &ctx},
	})

	if resp.Reasoning == nil || resp.Reasoning.Context == nil {
		t.Fatalf("expected reasoning.context, got %+v", resp.Reasoning)
	}
	if *resp.Reasoning.Context != ctx {
		t.Fatalf("expected context=%q, got %q", ctx, *resp.Reasoning.Context)
	}
}

// Event shape for response.function_call_arguments.done:
//
//	{type, sequence_number, item_id, output_index, arguments}
//
// wingman previously emitted an extra `name` field, which is not part of this
// event payload.
func TestFunctionCallArgumentsDoneEventHasNoNameField(t *testing.T) {
	data, err := json.Marshal(FunctionCallArgumentsDoneEvent{
		Type:           "response.function_call_arguments.done",
		SequenceNumber: 7,
		ItemID:         "fc_call_abc",
		OutputIndex:    2,
		Arguments:      "{}",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := got["name"]; ok {
		t.Fatalf("function_call_arguments.done must not carry `name`, got %s", data)
	}

	wantKeys := []string{"type", "sequence_number", "item_id", "output_index", "arguments"}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing required field %q in %s", k, data)
		}
	}
}

// Codex is strict about item lifecycle ordering. For the HAR-compatible path,
// each tool call should complete before the next one starts.
func TestParallelToolCallsStreamSerially(t *testing.T) {
	type ev struct {
		Type        StreamEventType
		ToolCallID  string
		OutputIndex int
	}

	var events []ev

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallAdded,
			StreamEventFunctionCallArgumentsDelta,
			StreamEventFunctionCallArgumentsDone,
			StreamEventFunctionCallDone:
			events = append(events, ev{Type: e.Type, ToolCallID: e.ToolCallID, OutputIndex: e.OutputIndex})
		}
		return nil
	})

	// Three tool calls arriving in three chunks, simulating an upstream
	// that streams them one after another (the HAR pattern).
	for _, call := range []provider.ToolCall{
		{ID: "c1", Name: "shell_command", Arguments: `{"command":"a"}`},
		{ID: "c2", Name: "shell_command", Arguments: `{"command":"b"}`},
		{ID: "c3", Name: "shell_command", Arguments: `{"command":"c"}`},
	} {
		c := call
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role:    provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(c)},
			},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Expect per call: added → delta → arguments.done → item.done.
	wantPerCall := []StreamEventType{
		StreamEventFunctionCallAdded,
		StreamEventFunctionCallArgumentsDelta,
		StreamEventFunctionCallArgumentsDone,
		StreamEventFunctionCallDone,
	}

	wantIDs := []string{"c1", "c2", "c3"}
	want := make([]ev, 0, len(wantIDs)*len(wantPerCall))
	for i, id := range wantIDs {
		for _, et := range wantPerCall {
			want = append(want, ev{Type: et, ToolCallID: id, OutputIndex: i})
		}
	}

	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d: %+v", len(want), len(events), events)
	}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("event %d: expected %+v, got %+v", i, w, events[i])
		}
	}
}

// Same property but verified against the wire-format event names that
// reach the client. Guards against future regressions where the
// accumulator order is right but the handler reorders or drops events.
func TestParallelToolCallsHaveSerialWireOrder(t *testing.T) {
	var wire []string

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallAdded:
			wire = append(wire, "response.output_item.added:"+e.ToolCallID)
		case StreamEventFunctionCallArgumentsDelta:
			wire = append(wire, "response.function_call_arguments.delta:"+e.ToolCallID)
		case StreamEventFunctionCallArgumentsDone:
			wire = append(wire, "response.function_call_arguments.done:"+e.ToolCallID)
		case StreamEventFunctionCallDone:
			wire = append(wire, "response.output_item.done:"+e.ToolCallID)
		}
		return nil
	})

	for _, id := range []string{"a", "b"} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      "shell_command",
					Arguments: `{}`,
				})},
			},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	want := []string{
		"response.output_item.added:a",
		"response.function_call_arguments.delta:a",
		"response.function_call_arguments.done:a",
		"response.output_item.done:a",
		"response.output_item.added:b",
		"response.function_call_arguments.delta:b",
		"response.function_call_arguments.done:b",
		"response.output_item.done:b",
	}

	if len(wire) != len(want) {
		t.Fatalf("expected %d wire events, got %d: %v", len(want), len(wire), wire)
	}
	for i, w := range want {
		if wire[i] != w {
			t.Errorf("wire[%d]: expected %q, got %q", i, w, wire[i])
		}
	}
}

// Tool call arguments arriving in multiple deltas for the same call must
// still result in a single added → multi-delta → done → item.done block —
// without an early close just because the call ID stayed the same.
func TestToolCallArgumentsFragmentedDeltasStayInOneBlock(t *testing.T) {
	var types []StreamEventType

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallAdded,
			StreamEventFunctionCallArgumentsDelta,
			StreamEventFunctionCallArgumentsDone,
			StreamEventFunctionCallDone:
			types = append(types, e.Type)
		}
		return nil
	})

	for _, frag := range []string{`{"co`, `mma`, `nd":"ls"}`} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
					ID:        "only",
					Name:      "shell_command",
					Arguments: frag,
				})},
			},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	want := []StreamEventType{
		StreamEventFunctionCallAdded,
		StreamEventFunctionCallArgumentsDelta,
		StreamEventFunctionCallArgumentsDelta,
		StreamEventFunctionCallArgumentsDelta,
		StreamEventFunctionCallArgumentsDone,
		StreamEventFunctionCallDone,
	}
	if len(types) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(types), types)
	}
	for i, w := range want {
		if types[i] != w {
			t.Errorf("event %d: expected %q, got %q", i, w, types[i])
		}
	}
}

// When the client sends `text` without `format`, the response must still
// include `format:{type:"text"}` — OpenAI always echoes a full text
// config.
func TestResponseDefaultsAddsTextFormatDefault(t *testing.T) {
	resp := &Response{}
	responseDefaults(resp, ResponsesRequest{
		Text: &TextConfig{Verbosity: VerbosityLow},
	})

	if resp.Text == nil {
		t.Fatal("expected text config")
	}
	if resp.Text.Format == nil {
		t.Fatal("expected text.format to default to {type:text}, got nil")
	}
	if resp.Text.Format.Type != "text" {
		t.Fatalf("expected text.format.type=text, got %q", resp.Text.Format.Type)
	}
	if resp.Text.Verbosity != VerbosityLow {
		t.Fatalf("expected verbosity to be preserved, got %q", resp.Text.Verbosity)
	}
}

// Smoke test for the per-turn request shape that Codex sends in the HAR
// captures. Includes the heterogeneous input array (message / reasoning /
// function_call / function_call_output) plus top-level fields wingman
// intentionally ignores (prompt_cache_key, client_metadata, etc.) — these
// must not break parsing.
func TestResponsesRequestParsesCodexTurnShape(t *testing.T) {
	payload := `{
		"model": "gpt-5.5",
		"instructions": "system prompt",
		"tools": [
			{"type": "function", "name": "shell_command", "strict": false, "parameters": {"type": "object", "properties": {}, "additionalProperties": false}},
			{"type": "custom", "name": "apply_patch", "format": {"type": "grammar", "syntax": "lark", "definition": "start: ..."}}
		],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"reasoning": {"effort": "medium"},
		"store": false,
		"stream": true,
		"include": ["reasoning.encrypted_content"],
		"prompt_cache_key": "019e6981-a5c5-7a21-9b58-a9ad0ff0e89b",
		"client_metadata": {"x-codex-installation-id": "5f31f59f"},
		"text": {"verbosity": "low"},
		"input": [
			{"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "rules"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "first message"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "ok"}]},
			{"type": "reasoning", "summary": [], "content": null, "encrypted_content": "AAA="},
			{"type": "function_call", "name": "shell_command", "arguments": "{\"command\":\"ls\"}", "call_id": "call_1"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok"}
		]
	}`

	var req ResponsesRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		t.Fatalf("unmarshal full request: %v", err)
	}

	if len(req.Input.Items) != 6 {
		t.Fatalf("expected 6 input items, got %d", len(req.Input.Items))
	}

	wantTypes := []InputItemType{
		InputItemTypeMessage,
		InputItemTypeMessage,
		InputItemTypeMessage,
		InputItemTypeReasoning,
		InputItemTypeFunctionCall,
		InputItemTypeFunctionCallOutput,
	}
	for i, want := range wantTypes {
		if req.Input.Items[i].Type != want {
			t.Errorf("item %d: expected type %q, got %q", i, want, req.Input.Items[i].Type)
		}
	}

	r := req.Input.Items[3].InputReasoning
	if r == nil || r.EncryptedContent != "AAA=" {
		t.Fatalf("reasoning item not parsed: %+v", req.Input.Items[3])
	}

	fc := req.Input.Items[4].InputFunctionCall
	if fc == nil || fc.Name != "shell_command" || fc.CallID != "call_1" {
		t.Fatalf("function_call item not parsed: %+v", req.Input.Items[4])
	}

	fco := req.Input.Items[5].InputFunctionCallOutput
	if fco == nil || fco.CallID != "call_1" {
		t.Fatalf("function_call_output item not parsed: %+v", req.Input.Items[5])
	}
	if len(fco.Output) != 1 || fco.Output[0].Text != "ok" {
		t.Fatalf("function_call_output.output not normalized: %+v", fco.Output)
	}

	if got := outputKind("apply_patch", req.Tools); got != provider.ToolKindCustom {
		t.Fatalf("expected Codex custom apply_patch tool to be wrapped as Custom, got %q", got)
	}
}

// A reasoning item with summary=[] and content=null (the shape Codex
// reflects back from a prior turn's `reasoning.encrypted_content`) must
// round-trip through toMessages without losing the encrypted signature.
func TestReasoningRoundTripFromCodexTurn(t *testing.T) {
	payload := `[
		{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "go"}]},
		{"type": "reasoning", "summary": [], "content": null, "encrypted_content": "ENC_ABC"},
		{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "done"}]}
	]`

	var input ResponsesInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	messages, err := toMessages(input.Items, "")
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(messages), messages)
	}

	var sawReasoning bool
	for _, c := range messages[1].Content {
		if c.Reasoning != nil && c.Reasoning.Signature == "ENC_ABC" {
			sawReasoning = true
		}
	}
	if !sawReasoning {
		t.Fatalf("expected reasoning with signature ENC_ABC in assistant message, got %+v", messages[1].Content)
	}
}

// Parallel tool calls (Codex emits three function_call items back-to-back)
// must collapse into a single assistant message with multiple tool calls.
func TestParallelFunctionCallsCollapseIntoOneAssistant(t *testing.T) {
	payload := `[
		{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "go"}]},
		{"type": "reasoning", "summary": [], "content": null, "encrypted_content": "ENC_1"},
		{"type": "function_call", "name": "shell_command", "arguments": "{\"command\":\"a\"}", "call_id": "c1"},
		{"type": "function_call", "name": "shell_command", "arguments": "{\"command\":\"b\"}", "call_id": "c2"},
		{"type": "function_call", "name": "shell_command", "arguments": "{\"command\":\"c\"}", "call_id": "c3"},
		{"type": "function_call_output", "call_id": "c1", "output": "1"},
		{"type": "function_call_output", "call_id": "c2", "output": "2"},
		{"type": "function_call_output", "call_id": "c3", "output": "3"}
	]`

	var input ResponsesInput
	if err := json.Unmarshal([]byte(payload), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	messages, err := toMessages(input.Items, "")
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(messages), messages)
	}

	var calls int
	for _, c := range messages[1].Content {
		if c.ToolCall != nil {
			calls++
		}
	}
	if calls != 3 {
		t.Fatalf("expected 3 tool calls in assistant message, got %d", calls)
	}

	var results int
	for _, c := range messages[2].Content {
		if c.ToolResult != nil {
			results++
		}
	}
	if results != 3 {
		t.Fatalf("expected 3 tool results in user message, got %d", results)
	}
}

// closeToolCall must be idempotent — Complete() calls it for every entry,
// including ones that were already closed inline when a later call started.
// Double-closing must not emit duplicate arguments.done / output_item.done.
func TestCloseToolCallIsIdempotent(t *testing.T) {
	var dones int

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		if e.Type == StreamEventFunctionCallArgumentsDone || e.Type == StreamEventFunctionCallDone {
			dones++
		}
		return nil
	})

	for _, id := range []string{"a", "b"} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      "shell_command",
					Arguments: `{}`,
				})},
			},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// 2 calls × 2 done events each = 4 total. If Complete re-closed call A
	// (already closed inline by B), we'd see 6.
	if dones != 4 {
		t.Fatalf("expected 4 total done events (2 per call), got %d", dones)
	}
}

// shouldClosePreviousToolCall must not fire if the previous call's args
// are not yet valid JSON. Otherwise a chunk that ends a previous call's
// JSON mid-stream would prematurely emit arguments.done.
func TestIncompleteJSONKeepsPreviousToolCallOpen(t *testing.T) {
	var order []string

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallAdded:
			order = append(order, "added:"+e.ToolCallID)
		case StreamEventFunctionCallArgumentsDone:
			order = append(order, "args_done:"+e.ToolCallID)
		case StreamEventFunctionCallDone:
			order = append(order, "item_done:"+e.ToolCallID)
		}
		return nil
	})

	// Chunk 1: call A starts with an incomplete JSON fragment.
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "a",
				Name:      "shell_command",
				Arguments: `{"command":`,
			})},
		},
	}); err != nil {
		t.Fatalf("Add A: %v", err)
	}

	// Chunk 2: call B starts with valid JSON. Because A's args are still
	// invalid, A must NOT be closed yet.
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "b",
				Name:      "shell_command",
				Arguments: `{}`,
			})},
		},
	}); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	// After two chunks, A should still be open. B's added arrives without
	// A having been closed inline.
	wantAfterAdds := []string{"added:a", "added:b"}
	if len(order) != len(wantAfterAdds) {
		t.Fatalf("after 2 Adds, expected %v, got %v", wantAfterAdds, order)
	}
	for i, w := range wantAfterAdds {
		if order[i] != w {
			t.Errorf("event %d: expected %q, got %q", i, w, order[i])
		}
	}

	// Complete then flushes both. closeToolCall always emits args.done
	// (even on invalid JSON) — this is the safe fallback.
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Final sequence after Complete: both calls closed, B first (still
	// open at Complete time, iterated in slice order: a, b → close a, close b).
	want := []string{
		"added:a", "added:b",
		"args_done:a", "item_done:a",
		"args_done:b", "item_done:b",
	}
	if len(order) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(order), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("event %d: expected %q, got %q", i, w, order[i])
		}
	}
}

// A delta with empty ID must route to the last tool call (via
// trackToolCall's lastToolCallID fallback) and must NOT trigger a close.
func TestEmptyToolCallIDDeltaContinuesLastCall(t *testing.T) {
	var addedIDs []string
	var deltaIDs []string

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallAdded:
			addedIDs = append(addedIDs, e.ToolCallID)
		case StreamEventFunctionCallArgumentsDelta:
			deltaIDs = append(deltaIDs, e.ToolCallID)
		}
		return nil
	})

	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "x",
				Name:      "shell_command",
				Arguments: `{"command":`,
			})},
		},
	}); err != nil {
		t.Fatalf("Add 1: %v", err)
	}
	// Subsequent chunk has no ID — must be treated as a continuation of x.
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "",
				Arguments: `"ls"}`,
			})},
		},
	}); err != nil {
		t.Fatalf("Add 2: %v", err)
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(addedIDs) != 1 || addedIDs[0] != "x" {
		t.Fatalf("expected exactly one added for id=x, got %v", addedIDs)
	}
	if len(deltaIDs) != 2 || deltaIDs[0] != "x" || deltaIDs[1] != "x" {
		t.Fatalf("expected both deltas routed to id=x, got %v", deltaIDs)
	}
}

// Reasoning followed by a tool call must close the reasoning item before
// any function_call events are emitted (closePendingItems contract).
func TestReasoningClosesBeforeToolCallStarts(t *testing.T) {
	var seq []StreamEventType

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventReasoningItemAdded,
			StreamEventReasoningItemDone,
			StreamEventFunctionCallAdded:
			seq = append(seq, e.Type)
		}
		return nil
	})

	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{
				ID:        "rs_1",
				Signature: "ENC_1",
			})},
		},
	}); err != nil {
		t.Fatalf("Add reasoning: %v", err)
	}

	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "c1",
				Name:      "shell_command",
				Arguments: `{}`,
			})},
		},
	}); err != nil {
		t.Fatalf("Add tool call: %v", err)
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	want := []StreamEventType{
		StreamEventReasoningItemAdded,
		StreamEventReasoningItemDone,
		StreamEventFunctionCallAdded,
	}
	if len(seq) != len(want) {
		t.Fatalf("expected %v, got %v", want, seq)
	}
	for i, w := range want {
		if seq[i] != w {
			t.Errorf("event %d: expected %q, got %q", i, w, seq[i])
		}
	}
}

// Result() must list tool calls in receive order — guards against any
// future reorder of s.toolCalls iteration.
func TestResultPreservesToolCallReceiveOrder(t *testing.T) {
	acc := NewStreamingAccumulator(func(StreamEvent) error { return nil })

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      "shell_command",
					Arguments: `{}`,
				})},
			},
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := acc.Result()
	var ids []string
	for _, c := range got.Message.Content {
		if c.ToolCall != nil {
			ids = append(ids, c.ToolCall.ID)
		}
	}

	want := []string{"alpha", "beta", "gamma"}
	if len(ids) != len(want) {
		t.Fatalf("expected %v tool calls, got %v", want, ids)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("tool call %d: expected %q, got %q", i, w, ids[i])
		}
	}
}

// End-to-end wire-order pin matching the HAR pattern: reasoning → text →
// two function calls. Any future regression in event ordering will fail
// here loudly.
func TestEndToEndStreamingSequenceMatchesHARPattern(t *testing.T) {
	var wire []string

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventReasoningItemAdded:
			wire = append(wire, "reasoning.added")
		case StreamEventReasoningItemDone:
			wire = append(wire, "reasoning.done")
		case StreamEventOutputItemAdded:
			wire = append(wire, "message.added")
		case StreamEventContentPartAdded:
			wire = append(wire, "message.part.added")
		case StreamEventTextDelta:
			wire = append(wire, "message.text.delta")
		case StreamEventTextDone:
			wire = append(wire, "message.text.done")
		case StreamEventContentPartDone:
			wire = append(wire, "message.part.done")
		case StreamEventOutputItemDone:
			wire = append(wire, "message.done")
		case StreamEventFunctionCallAdded:
			wire = append(wire, "fc.added:"+e.ToolCallID)
		case StreamEventFunctionCallArgumentsDelta:
			wire = append(wire, "fc.args.delta:"+e.ToolCallID)
		case StreamEventFunctionCallArgumentsDone:
			wire = append(wire, "fc.args.done:"+e.ToolCallID)
		case StreamEventFunctionCallDone:
			wire = append(wire, "fc.done:"+e.ToolCallID)
		case StreamEventResponseCompleted:
			wire = append(wire, "response.completed")
		}
		return nil
	})

	// Reasoning.
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ReasoningContent(provider.Reasoning{
				ID:        "rs_1",
				Signature: "ENC_1",
			})},
		},
	}); err != nil {
		t.Fatalf("Add reasoning: %v", err)
	}

	// Final assistant text.
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.TextContent("plan: ")},
		},
	}); err != nil {
		t.Fatalf("Add text 1: %v", err)
	}
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: []provider.Content{provider.TextContent("run ls")},
		},
	}); err != nil {
		t.Fatalf("Add text 2: %v", err)
	}

	// Two function calls.
	for _, id := range []string{"c1", "c2"} {
		if err := acc.Add(provider.Completion{
			Message: &provider.Message{
				Role: provider.MessageRoleAssistant,
				Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
					ID:        id,
					Name:      "shell_command",
					Arguments: `{}`,
				})},
			},
		}); err != nil {
			t.Fatalf("Add tool call %s: %v", id, err)
		}
	}

	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	want := []string{
		"reasoning.added",
		"reasoning.done",
		"message.added",
		"message.part.added",
		"message.text.delta",
		"message.text.delta",
		"message.text.done",
		"message.part.done",
		"message.done",
		"fc.added:c1",
		"fc.args.delta:c1",
		"fc.args.done:c1",
		"fc.done:c1",
		"fc.added:c2",
		"fc.args.delta:c2",
		"fc.args.done:c2",
		"fc.done:c2",
		"response.completed",
	}

	if len(wire) != len(want) {
		t.Fatalf("expected %d wire events, got %d:\nwant: %v\ngot:  %v", len(want), len(wire), want, wire)
	}
	for i, w := range want {
		if wire[i] != w {
			t.Errorf("event %d: expected %q, got %q", i, w, wire[i])
		}
	}
}

// Computer and apply_patch are virtual tools whose output is emitted as a
// distinct item type from FunctionCallDone. The accumulator still produces
// FunctionCallArgumentsDelta/Done events for them (it does not know about
// tool-type semantics), but the handler must suppress those at the wire —
// otherwise the client receives function_call_arguments.* events referring
// to an fc_<id> item that was never added.
func TestApplyPatchSuppressFunctionCallArgumentsEvents(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		tools    []Tool
	}{
		{
			name:     "apply_patch",
			toolName: "apply_patch",
			tools:    []Tool{{Type: ToolTypeApplyPatch}},
		},
		{
			name:     "str_replace_based_edit_tool",
			toolName: "str_replace_based_edit_tool",
			tools:    []Tool{{Type: ToolTypeApplyPatch}},
		},
		{
			name:     "custom_apply_patch",
			toolName: "apply_patch",
			tools:    []Tool{{Type: ToolTypeCustom, Name: "apply_patch"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := responseOutputOptions{Tools: tc.tools}
			if opts.kindOf(tc.toolName) == provider.ToolKindFunction {
				t.Fatalf("tool %q should resolve to a non-function Kind, got %q", tc.toolName, opts.kindOf(tc.toolName))
			}
		})
	}
}

// End-to-end variant of the suppression check: drive an apply_patch tool
// call through the accumulator and the handler's event-routing logic (a
// small stand-in for handleResponsesStream), and assert the handler emits
// no function_call_arguments.* wire events for it. The accumulator still
// emits the internal StreamEvent — the suppression happens at the wire
// layer in handler_responses.go, keyed by the tool's registered Kind.
func TestApplyPatchToolCallEmitsNoArgumentsWireEvents(t *testing.T) {
	type wireEv struct {
		Type string
	}

	var wire []wireEv

	opts := responseOutputOptions{Tools: []Tool{
		{Type: ToolTypeApplyPatch},
	}}

	acc := NewStreamingAccumulator(func(e StreamEvent) error {
		switch e.Type {
		case StreamEventFunctionCallArgumentsDelta:
			if opts.kindOf(e.ToolCallName) != provider.ToolKindFunction {
				return nil
			}
			wire = append(wire, wireEv{Type: "response.function_call_arguments.delta"})
		case StreamEventFunctionCallArgumentsDone:
			if opts.kindOf(e.ToolCallName) != provider.ToolKindFunction {
				return nil
			}
			wire = append(wire, wireEv{Type: "response.function_call_arguments.done"})
		}
		return nil
	})

	args := `{"type":"update_file","path":"main.go","diff":"@@\n-old\n+new\n"}`
	if err := acc.Add(provider.Completion{
		Message: &provider.Message{
			Role: provider.MessageRoleAssistant,
			Content: []provider.Content{provider.ToolCallContent(provider.ToolCall{
				ID:        "apc_1",
				Name:      "apply_patch",
				Arguments: args,
			})},
		},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := acc.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(wire) != 0 {
		t.Fatalf("expected zero function_call_arguments.* wire events for apply_patch, got %d: %+v", len(wire), wire)
	}
}
