package hard_test

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
)

// Flow scenarios drive complete turns on the reference API and on Wingman
// with every configured backend, then replay the produced content verbatim,
// signed thinking included. Beyond the outcome they compare the wire shape:
// fields per content block type, fields per SSE event type, and the streamed
// message against the non-streamed response of the same target.

type messageRound struct {
	response map[string]any
	events   []*harness.SSEEvent
}

func (r messageRound) content() []any {
	content, _ := r.response["content"].([]any)
	return content
}

func runMessageRound(t *testing.T, h *anthropic.Harness, tg target, body map[string]any, stream bool) messageRound {
	t.Helper()

	if !stream {
		return messageRound{response: post(t, h, tg, body)}
	}

	events := postSSE(t, h, tg, body)
	return messageRound{response: assembleMessage(t, tg.label, events), events: events}
}

// assembleMessage rebuilds the final message from a stream the way an SDK
// does, so the streamed result can be checked like a non-streamed response.
func assembleMessage(t *testing.T, label string, events []*harness.SSEEvent) map[string]any {
	t.Helper()

	var message map[string]any
	var blocks []any
	stopped := false

	for _, event := range events {
		switch event.Event {
		case "message_start":
			message, _ = event.Data["message"].(map[string]any)
			if message == nil {
				t.Fatalf("[%s] message_start without message: %v", label, event.Data)
			}

		case "content_block_start":
			index := int(event.Data["index"].(float64))
			if index != len(blocks) {
				t.Fatalf("[%s] content_block_start index %d, want %d", label, index, len(blocks))
			}
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" || block["type"] == "server_tool_use" {
				block["partial_json"] = ""
			}
			blocks = append(blocks, block)

		case "content_block_delta":
			index := int(event.Data["index"].(float64))
			if index >= len(blocks) {
				t.Fatalf("[%s] delta for unknown block %d", label, index)
			}
			block := blocks[index].(map[string]any)
			delta, _ := event.Data["delta"].(map[string]any)
			for _, field := range []string{"text", "thinking", "signature"} {
				if value, ok := delta[field].(string); ok {
					previous, _ := block[field].(string)
					block[field] = previous + value
				}
			}
			if value, ok := delta["partial_json"].(string); ok {
				previous, _ := block["partial_json"].(string)
				block["partial_json"] = previous + value
			}

		case "message_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			for key, value := range delta {
				message[key] = value
			}
			if usage, ok := event.Data["usage"].(map[string]any); ok {
				current, _ := message["usage"].(map[string]any)
				if current == nil {
					current = map[string]any{}
					message["usage"] = current
				}
				for key, value := range usage {
					current[key] = value
				}
			}

		case "message_stop":
			stopped = true

		case "error":
			t.Fatalf("[%s] stream error: %v", label, event.Data)
		}
	}

	if message == nil || !stopped {
		t.Fatalf("[%s] stream did not deliver a complete message", label)
	}

	for _, raw := range blocks {
		block := raw.(map[string]any)
		if partial, ok := block["partial_json"].(string); ok {
			delete(block, "partial_json")
			if partial != "" {
				var input any
				if err := parseJSON(partial, &input); err != nil {
					t.Fatalf("[%s] streamed tool input is not JSON: %q (%v)", label, partial, err)
				}
				block["input"] = input
			}
		}
	}
	message["content"] = blocks

	return message
}

// rejectedStream reports a stream that never started: an HTTP error body or
// an error event before message_start.
func rejectedStream(events []*harness.SSEEvent) (bool, string) {
	for _, event := range events {
		switch {
		case event.Event == "message_start":
			return false, ""
		case event.Event == "error", event.Data["type"] == "error":
			return true, fmt.Sprint(event.Data)
		}
	}
	return true, fmt.Sprintf("%d events without message_start", len(events))
}

func blocksOfType(content []any, kind string) []map[string]any {
	var blocks []map[string]any
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] == kind {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// requireToolTurn checks a turn that should announce and then call the
// weather tool: one valid tool_use, signed thinking when thinking is on, and
// the tool_use stop reason.
func requireToolTurn(t *testing.T, label string, response map[string]any, thinking bool) {
	t.Helper()

	content, _ := response["content"].([]any)
	uses := blocksOfType(content, "tool_use")
	if len(uses) != 1 || uses[0]["name"] != "get_weather" {
		t.Fatalf("[%s] expected exactly one get_weather tool_use, got %v", label, content)
	}
	if id, _ := uses[0]["id"].(string); id == "" {
		t.Errorf("[%s] tool_use without id: %v", label, uses[0])
	}
	input, _ := uses[0]["input"].(map[string]any)
	if location, _ := input["location"].(string); location == "" {
		t.Errorf("[%s] tool_use input without location: %v", label, uses[0])
	}
	if response["stop_reason"] != "tool_use" {
		t.Errorf("[%s] stop_reason = %v, want tool_use", label, response["stop_reason"])
	}
	if len(blocksOfType(content, "text")) == 0 {
		t.Logf("[%s] model called the tool without announcing it first; text-before-tool replay not exercised", label)
	}
	requireSignedThinking(t, label, content)
	if thinking && len(blocksOfType(content, "thinking")) == 0 && len(blocksOfType(content, "redacted_thinking")) == 0 {
		t.Logf("[%s] thinking enabled but no thinking block returned", label)
	}
}

// requireAnswerTurn checks the turn after the tool result: text that uses the
// result, no further tool_use, and the end_turn stop reason.
func requireAnswerTurn(t *testing.T, label string, response map[string]any) {
	t.Helper()

	content, _ := response["content"].([]any)
	if len(blocksOfType(content, "tool_use")) > 0 {
		t.Fatalf("[%s] model requested another tool instead of answering: %v", label, content)
	}
	if response["stop_reason"] != "end_turn" {
		t.Errorf("[%s] stop_reason = %v, want end_turn", label, response["stop_reason"])
	}
	requireSignedThinking(t, label, content)

	answer := strings.ToLower(messageText(response))
	if answer == "" {
		t.Fatalf("[%s] no text in the answer: %v", label, content)
	}
	if !strings.Contains(answer, "sunny") && !strings.Contains(answer, "22") {
		t.Errorf("[%s] answer does not use the tool result: %q", label, answer)
	}
}

// requireSignedThinking checks that thinking blocks carry a signature and
// that it survived JSON encoding: a replacement character means the backend's
// signature bytes were written into the string unencoded.
func requireSignedThinking(t *testing.T, label string, content []any) {
	t.Helper()

	for _, block := range blocksOfType(content, "thinking") {
		signature, _ := block["signature"].(string)
		if signature == "" {
			t.Errorf("[%s] thinking block without signature cannot be replayed: %v", label, block)
		}
		if strings.ContainsRune(signature, '\uFFFD') {
			t.Errorf("[%s] thinking signature corrupted on the wire: %q", label, signature)
		}
	}
}

func toolResults(content []any) []any {
	var results []any
	for _, use := range blocksOfType(content, "tool_use") {
		name, _ := use["name"].(string)
		args, _ := marshalJSON(use["input"])
		results = append(results, map[string]any{
			"type":        "tool_result",
			"tool_use_id": use["id"],
			"content":     hardtest.ToolResult(name, args),
		})
	}
	return results
}

// compareMessageFields compares the JSON fields of the message, of its usage,
// and of its content blocks by block type. Counts, order and values vary
// between runs and models; the field set of each type must not.
func compareMessageFields(t *testing.T, label string, reference, actual map[string]any) {
	t.Helper()

	compareFieldSets(t, label+"/message", objectFields(reference), objectFields(actual))

	refUsage, _ := reference["usage"].(map[string]any)
	actUsage, _ := actual["usage"].(map[string]any)
	compareFieldSets(t, label+"/usage", objectFields(refUsage), objectFields(actUsage))

	refContent, _ := reference["content"].([]any)
	actContent, _ := actual["content"].([]any)
	want, got := blockFields(refContent), blockFields(actContent)
	for kind, wantKeys := range want {
		gotKeys, ok := got[kind]
		if !ok {
			t.Logf("[%s] reference has %s blocks, actual has none", label, kind)
			continue
		}
		compareFieldSets(t, label+"/"+kind, wantKeys, gotKeys)
	}
	for kind := range got {
		if _, ok := want[kind]; !ok {
			t.Logf("[%s] actual has %s blocks, reference has none", label, kind)
		}
	}
}

// compareEventFields compares the fields of SSE events by event type, and of
// streamed content blocks and deltas by their type. Event types only one side
// emits are logged; a shared event must carry the same fields.
func compareEventFields(t *testing.T, label string, reference, actual []*harness.SSEEvent) {
	t.Helper()

	want, got := eventFields(reference), eventFields(actual)
	for kind, wantKeys := range want {
		gotKeys, ok := got[kind]
		if !ok {
			t.Logf("[%s] reference emits %s, actual never does", label, kind)
			continue
		}
		compareFieldSets(t, label+"/"+kind, wantKeys, gotKeys)
	}
	for kind := range got {
		if _, ok := want[kind]; !ok {
			t.Logf("[%s] actual emits %s, reference never does", label, kind)
		}
	}
}

// optionalUsageFields describe the Anthropic service rather than the turn
// (tier, geo, cache TTL breakdown) or depend on thinking being active; the
// gateway reports them only when the backend does.
var optionalUsageFields = map[string]bool{"cache_creation": true, "service_tier": true, "inference_geo": true, "output_tokens_details": true}

func compareFieldSets(t *testing.T, label string, want, got map[string]bool) {
	t.Helper()

	usage := strings.HasSuffix(label, "/usage")
	for key := range want {
		if !got[key] && !(usage && optionalUsageFields[key]) {
			t.Errorf("[%s] lacks field %q present on the reference (reference %v, actual %v)", label, key, keys(want), keys(got))
		}
	}
	for key := range got {
		if !want[key] && !(usage && optionalUsageFields[key]) {
			t.Errorf("[%s] has field %q absent on the reference (reference %v, actual %v)", label, key, keys(want), keys(got))
		}
	}
}

func objectFields(object map[string]any) map[string]bool {
	fields := map[string]bool{}
	for key := range object {
		fields[key] = true
	}
	return fields
}

func blockFields(content []any) map[string]map[string]bool {
	fields := map[string]map[string]bool{}
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		kind, _ := block["type"].(string)
		if fields[kind] == nil {
			fields[kind] = map[string]bool{}
		}
		for key := range block {
			fields[kind][key] = true
		}
	}
	return fields
}

func eventFields(events []*harness.SSEEvent) map[string]map[string]bool {
	fields := map[string]map[string]bool{}
	record := func(kind string, object map[string]any) {
		if fields[kind] == nil {
			fields[kind] = map[string]bool{}
		}
		for key := range object {
			fields[kind][key] = true
		}
	}
	for _, event := range events {
		if event.Data == nil || event.Event == "ping" {
			continue
		}
		record(event.Event, event.Data)
		if block, ok := event.Data["content_block"].(map[string]any); ok {
			kind, _ := block["type"].(string)
			record(event.Event+"."+kind, block)
		}
		if delta, ok := event.Data["delta"].(map[string]any); ok {
			kind, _ := delta["type"].(string)
			if kind == "" {
				kind = "message"
			}
			record(event.Event+"."+kind, delta)
		}
	}
	return fields
}

func keys(set map[string]bool) []string {
	var list []string
	for key := range set {
		list = append(list, key)
	}
	sort.Strings(list)
	return list
}

type messageRun struct {
	call, answer messageRound
}

// TestFlowTextThenToolLoop: the model announces a tool call, calls it, and
// the whole content (thinking, text, tool_use) is replayed with the result.
// Checks each turn's shape and that every backend accepts the replay.
func TestFlowTextThenToolLoop(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 2048,
				"messages":   user(hardtest.TextThenToolPrompt),
				"tools":      []any{anthropic.WeatherTool},
			}
			if model.Capabilities.Thinking {
				body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 1024}
			}

			runs := map[string]map[bool]messageRun{}
			for _, tg := range targets(h, model) {
				runs[tg.label] = map[bool]messageRun{}
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%t", tg.label, stream), func(t *testing.T) {
						call := runMessageRound(t, h, tg, body, stream)
						requireToolTurn(t, tg.label, call.response, model.Capabilities.Thinking)

						replay := map[string]any{}
						for key, value := range body {
							replay[key] = value
						}
						messages := append([]any{}, body["messages"].([]any)...)
						messages = append(messages, map[string]any{"role": "assistant", "content": call.content()})
						messages = append(messages, map[string]any{"role": "user", "content": toolResults(call.content())})
						replay["messages"] = messages

						answer := runMessageRound(t, h, tg, replay, stream)
						requireAnswerTurn(t, tg.label, answer.response)

						runs[tg.label][stream] = messageRun{call: call, answer: answer}
					})
				}
			}

			reference, wingman := runs["anthropic"], runs["wingman"]
			for _, stream := range []bool{false, true} {
				r, w := reference[stream], wingman[stream]
				if r.call.response == nil || w.call.response == nil {
					continue
				}
				label := fmt.Sprintf("stream=%t", stream)
				compareMessageFields(t, label+"/call", r.call.response, w.call.response)
				compareMessageFields(t, label+"/answer", r.answer.response, w.answer.response)
				if stream {
					compareEventFields(t, "call", r.call.events, w.call.events)
					compareEventFields(t, "answer", r.answer.events, w.answer.events)
				}
			}
			for label, byStream := range runs {
				plain, streamed := byStream[false], byStream[true]
				if plain.call.response == nil || streamed.call.response == nil {
					continue
				}
				compareMessageFields(t, label+"/snapshot/call", plain.call.response, streamed.call.response)
				compareMessageFields(t, label+"/snapshot/answer", plain.answer.response, streamed.answer.response)
			}
		})
	}
}

// TestFlowSystemAfterToolResult: a system message follows the tool result, as
// Claude Code sends mid-conversation instructions. Backends whose native API
// takes instructions only at the top must still accept the turn. The
// reference comparison is skipped when the reference model rejects the shape.
func TestFlowSystemAfterToolResult(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 1024,
				"tools":      []any{anthropic.WeatherTool},
				"messages": []any{
					map[string]any{"role": "user", "content": "What's the weather in Bern?"},
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_flow", "name": "get_weather", "input": map[string]any{"location": "Bern, Switzerland"}}}},
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_flow", "content": hardtest.ToolResult("get_weather", `{"location":"Bern, Switzerland"}`)}}},
					map[string]any{"role": "system", "content": "Reply in one short sentence, in English."},
				},
			}

			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
					var reference map[string]any
					var referenceEvents []*harness.SSEEvent

					request := anthropic.WithModel(body, h.ReferenceModel)
					if stream {
						request["stream"] = true
						referenceEvents = anthropic.PostMessagesSSE(t, h, h.Anthropic, request)
						// A rejected request answers with a JSON error, not a stream.
						if rejected, detail := rejectedStream(referenceEvents); rejected {
							t.Logf("reference rejected a system message after the tool result: %s", detail)
						} else {
							reference = assembleMessage(t, "anthropic", referenceEvents)
						}
					} else {
						resp := anthropic.PostMessages(t, h, h.Anthropic, request)
						if resp.StatusCode != 200 {
							t.Logf("reference rejected a system message after the tool result: %s", resp.RawBody)
						} else {
							reference = resp.Body
						}
					}

					tg := target{"wingman", h.Wingman, model.Name}
					round := runMessageRound(t, h, tg, body, stream)
					requireAnswerTurn(t, tg.label, round.response)

					if reference == nil {
						return
					}
					requireAnswerTurn(t, "anthropic", reference)
					compareMessageFields(t, "answer", reference, round.response)
					if stream {
						compareEventFields(t, "answer", referenceEvents, round.events)
					}
				})
			}
		})
	}
}

// TestFlowPromptCacheAcrossTurns keeps a long system prefix stable across a
// tool loop. Backends that cache prompts must report cache reads on every
// turn after the first, through Wingman as natively. The client marks the
// prefix as the Messages API requires; the gateway caches the prefix by
// default on every backend that supports it.
func TestFlowPromptCacheAcrossTurns(t *testing.T) {
	h := anthropic.New(t)

	for _, model := range anthropic.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"max_tokens": 1024,
				"system":     []any{map[string]any{"type": "text", "text": hardtest.CachePrefix(), "cache_control": map[string]any{"type": "ephemeral"}}},
				"tools":      []any{anthropic.WeatherTool, clockTool},
				"messages":   user(hardtest.ChainedPrompt),
			}

			for _, tg := range targets(h, model) {
				t.Run(tg.label, func(t *testing.T) {
					cached := cacheReadsPerTurn(t, h, tg, body)
					requireCachedTurns(t, tg.label, cached, model.Capabilities.Cache, tg.label == "anthropic" || deterministicCache(model.Name))
				})
			}
		})
	}
}

// deterministicCache reports backends whose prompt cache answers every
// matching prefix within its TTL. OpenAI's cache is best effort and may miss
// a turn under load, so only one cached turn can be required there.
func deterministicCache(model string) bool {
	return strings.Contains(model, "claude") || strings.Contains(model, "bedrock")
}

// requireCachedTurns checks the cache reads of a tool loop: every turn after
// the first on deterministic backends, at least one otherwise.
func requireCachedTurns(t *testing.T, label string, cached []float64, expected, deterministic bool) {
	t.Helper()

	if len(cached) < 2 {
		t.Fatalf("[%s] tool loop finished in %d turn(s); need at least two", label, len(cached))
	}
	hits := 0
	for turn, tokens := range cached {
		t.Logf("[%s] turn %d: cache_read_input_tokens %v", label, turn+1, tokens)
		if turn == 0 {
			continue
		}
		if tokens > 0 {
			hits++
		} else if expected && deterministic {
			t.Errorf("[%s] turn %d: no cache read although the prefix did not change", label, turn+1)
		}
	}
	if expected && hits == 0 {
		t.Errorf("[%s] no turn reported a cache read", label)
	}
}

// cacheReadsPerTurn drives the tool loop, replaying content verbatim, and
// returns each turn's cache_read_input_tokens.
func cacheReadsPerTurn(t *testing.T, h *anthropic.Harness, tg target, body map[string]any) []float64 {
	t.Helper()

	messages := append([]any{}, body["messages"].([]any)...)
	var cached []float64

	for round := 0; round < 6; round++ {
		req := maps.Clone(body)
		req["messages"] = messages

		resp := post(t, h, tg, req)
		usage, _ := resp["usage"].(map[string]any)
		read, _ := usage["cache_read_input_tokens"].(float64)
		cached = append(cached, read)

		content, _ := resp["content"].([]any)
		if len(toolUses(resp)) == 0 {
			return cached
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": content}, map[string]any{"role": "user", "content": toolResults(content)})
	}

	t.Fatalf("[%s] tool loop did not finish within 6 rounds", tg.label)
	return nil
}
