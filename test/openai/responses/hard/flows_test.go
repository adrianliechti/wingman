package hard_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
)

// Flow scenarios drive complete turns on the reference API and on Wingman
// with every configured backend, then replay the produced output verbatim.
// Beyond the outcome they compare the wire shape: fields per output item
// type, fields per SSE event type, the stream lifecycle, and the streamed
// snapshot against the non-streamed response of the same target.

type flowRound struct {
	response map[string]any
	events   []*harness.SSEEvent
}

func (r flowRound) output() []any {
	output, _ := r.response["output"].([]any)
	return output
}

func runFlowRound(t *testing.T, h *openai.Harness, tg target, body map[string]any, stream bool) flowRound {
	t.Helper()

	if !stream {
		return flowRound{response: post(t, h, tg, body)}
	}

	events := postSSE(t, h, tg, body)
	requireStreamItemLifecycle(t, events)

	for _, event := range events {
		switch sseType(event) {
		case "response.completed", "response.incomplete":
			response, _ := event.Data["response"].(map[string]any)
			return flowRound{response: response, events: events}
		}
	}

	t.Fatalf("[%s] stream ended without a terminal response", tg.label)
	return flowRound{}
}

func sseType(e *harness.SSEEvent) string {
	if e.Event != "" {
		return e.Event
	}
	kind, _ := e.Data["type"].(string)
	return kind
}

func itemsOfType(output []any, kind string) []map[string]any {
	var items []map[string]any
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		if item["type"] == kind {
			items = append(items, item)
		}
	}
	return items
}

// requireToolRound checks a turn that should announce and then call the
// weather tool: one valid function call, well-formed message items, and
// well-formed reasoning items when present.
func requireToolRound(t *testing.T, label string, output []any) {
	t.Helper()

	calls := itemsOfType(output, "function_call")
	if len(calls) != 1 || calls[0]["name"] != "get_weather" {
		t.Fatalf("[%s] expected exactly one get_weather call, got %v", label, output)
	}
	call := calls[0]
	if id, _ := call["call_id"].(string); id == "" {
		t.Errorf("[%s] function_call without call_id: %v", label, call)
	}
	var args struct {
		Location string `json:"location"`
	}
	arguments, _ := call["arguments"].(string)
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args.Location == "" {
		t.Errorf("[%s] function_call arguments not a location object: %q (%v)", label, arguments, err)
	}

	messages := itemsOfType(output, "message")
	if len(messages) == 0 {
		t.Logf("[%s] model called the tool without announcing it first; commentary replay not exercised", label)
	}
	for _, message := range messages {
		requireMessageItem(t, label, message)
	}
	requireReasoningItems(t, label, output)
}

// requireReasoningItems checks the shape of reasoning items and that their
// opaque state survived JSON encoding: a replacement character means the
// backend's signature bytes were written into the string unencoded.
func requireReasoningItems(t *testing.T, label string, output []any) {
	t.Helper()

	for _, reasoning := range itemsOfType(output, "reasoning") {
		if id, _ := reasoning["id"].(string); id == "" {
			t.Errorf("[%s] reasoning item without id: %v", label, reasoning)
		}
		if _, ok := reasoning["summary"].([]any); !ok {
			t.Errorf("[%s] reasoning item without summary array: %v", label, reasoning)
		}
		if encrypted, _ := reasoning["encrypted_content"].(string); strings.ContainsRune(encrypted, '\uFFFD') {
			t.Errorf("[%s] reasoning encrypted_content corrupted on the wire: %q", label, encrypted)
		}
	}
}

func requireMessageItem(t *testing.T, label string, message map[string]any) {
	t.Helper()

	if id, _ := message["id"].(string); id == "" {
		t.Errorf("[%s] message item without id: %v", label, message)
	}
	if message["role"] != "assistant" || message["status"] != "completed" {
		t.Errorf("[%s] message item role/status: %v", label, message)
	}
	if phase, ok := message["phase"]; ok && phase != "commentary" && phase != "final_answer" && phase != nil {
		t.Errorf("[%s] message item with unknown phase %v", label, phase)
	}
	content, _ := message["content"].([]any)
	var text string
	for _, raw := range content {
		part, _ := raw.(map[string]any)
		if part["type"] == "output_text" {
			s, _ := part["text"].(string)
			text += s
		}
	}
	if text == "" {
		t.Errorf("[%s] message item without output_text: %v", label, message)
	}
}

// requireAnswerRound checks the turn after the tool result: text that uses
// the result and no further call.
func requireAnswerRound(t *testing.T, label string, response map[string]any) {
	t.Helper()

	output, _ := response["output"].([]any)
	if len(itemsOfType(output, "function_call")) > 0 {
		t.Fatalf("[%s] model requested another tool instead of answering: %v", label, output)
	}
	messages := itemsOfType(output, "message")
	if len(messages) == 0 {
		t.Fatalf("[%s] no message item in the answer: %v", label, output)
	}
	for _, message := range messages {
		requireMessageItem(t, label, message)
	}
	requireReasoningItems(t, label, output)
	if phases := messagePhases(messages); len(phases) > 0 && phases[len(phases)-1] == "commentary" {
		t.Errorf("[%s] answer ended with a commentary message: %v", label, phases)
	}

	answer := strings.ToLower(messageText(response))
	if !strings.Contains(answer, "sunny") && !strings.Contains(answer, "22") {
		t.Errorf("[%s] answer does not use the tool result: %q", label, answer)
	}
}

func messagePhases(messages []map[string]any) []string {
	var phases []string
	for _, message := range messages {
		phase, _ := message["phase"].(string)
		phases = append(phases, phase)
	}
	return phases
}

func toolOutputs(output []any) []any {
	var results []any
	for _, call := range itemsOfType(output, "function_call") {
		name, _ := call["name"].(string)
		arguments, _ := call["arguments"].(string)
		results = append(results, map[string]any{
			"type":    "function_call_output",
			"call_id": call["call_id"],
			"output":  hardtest.ToolResult(name, arguments),
		})
	}
	return results
}

// optionalFields are emitted by one side only without being a translation
// defect, keyed by item or event type ("*" applies to every type): phase is a
// GPT-5 message field; the live API omits status on reasoning items and name
// on function_call_arguments.done although both are in the schema;
// obfuscation is OpenAI's payload padding.
var optionalFields = map[string]map[string]bool{
	"*":                                     {"obfuscation": true},
	"message":                               {"phase": true},
	"reasoning":                             {"status": true},
	"response.function_call_arguments.done": {"name": true},
}

func optional(kind, key string) bool {
	return optionalFields["*"][key] || optionalFields[kind][key]
}

// compareItemFields compares the JSON fields of output items by item type,
// and of message content parts by part type. Item counts, order and values
// vary between runs and models; the field set of each type must not.
func compareItemFields(t *testing.T, label string, reference, actual []any) {
	t.Helper()

	want, got := itemFields(reference), itemFields(actual)
	for kind, wantKeys := range want {
		gotKeys, ok := got[kind]
		if !ok {
			t.Logf("[%s] reference has %s items, actual has none", label, kind)
			continue
		}
		for key := range wantKeys {
			if !gotKeys[key] && !optional(kind, key) {
				t.Errorf("[%s] %s item lacks field %q present on the reference (reference %v, actual %v)", label, kind, key, keys(wantKeys), keys(gotKeys))
			}
		}
		for key := range gotKeys {
			if !wantKeys[key] && !optional(kind, key) {
				t.Errorf("[%s] %s item has field %q absent on the reference (reference %v, actual %v)", label, kind, key, keys(wantKeys), keys(gotKeys))
			}
		}
	}
	for kind := range got {
		if _, ok := want[kind]; !ok {
			t.Logf("[%s] actual has %s items, reference has none", label, kind)
		}
	}
}

func itemFields(output []any) map[string]map[string]bool {
	fields := map[string]map[string]bool{}
	record := func(kind string, object map[string]any) {
		if fields[kind] == nil {
			fields[kind] = map[string]bool{}
		}
		for key := range object {
			fields[kind][key] = true
		}
	}
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		kind, _ := item["type"].(string)
		record(kind, item)
		content, _ := item["content"].([]any)
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			partKind, _ := part["type"].(string)
			record(kind+"."+partKind, part)
		}
	}
	return fields
}

// compareEventFields compares the top-level fields of SSE events by event
// type. Event types only one side emits are logged: reasoning summaries and
// commentary depend on the backend, but a shared event must carry the same
// fields.
func compareEventFields(t *testing.T, label string, reference, actual []*harness.SSEEvent) {
	t.Helper()

	want, got := eventFields(reference), eventFields(actual)
	for kind, wantKeys := range want {
		gotKeys, ok := got[kind]
		if !ok {
			t.Logf("[%s] reference emits %s, actual never does", label, kind)
			continue
		}
		for key := range wantKeys {
			if !gotKeys[key] && !optional(kind, key) {
				t.Errorf("[%s] %s lacks field %q present on the reference (reference %v, actual %v)", label, kind, key, keys(wantKeys), keys(gotKeys))
			}
		}
		for key := range gotKeys {
			if !wantKeys[key] && !optional(kind, key) {
				t.Errorf("[%s] %s has field %q absent on the reference (reference %v, actual %v)", label, kind, key, keys(wantKeys), keys(gotKeys))
			}
		}
	}
	for kind := range got {
		if _, ok := want[kind]; !ok {
			t.Logf("[%s] actual emits %s, reference never does", label, kind)
		}
	}
}

func eventFields(events []*harness.SSEEvent) map[string]map[string]bool {
	fields := map[string]map[string]bool{}
	for _, event := range events {
		if event.Data == nil {
			continue
		}
		kind := sseType(event)
		if fields[kind] == nil {
			fields[kind] = map[string]bool{}
		}
		for key := range event.Data {
			fields[kind][key] = true
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

type flowRun struct {
	call, answer flowRound
}

// TestFlowCommentaryToolLoop: the model announces a tool call, calls it, and
// the whole output (reasoning, message, function_call) is replayed with the
// result. Checks each round's shape and that every backend accepts the replay.
func TestFlowCommentaryToolLoop(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store":   false,
				"tools":   []any{weatherTool},
				"include": []string{"reasoning.encrypted_content"},
				"input":   userInput(hardtest.TextThenToolPrompt),
			}
			if model.Capabilities.Thinking {
				body["reasoning"] = map[string]any{"effort": "low", "summary": "auto"}
			}

			runs := map[string]map[bool]flowRun{}
			for _, tg := range targets(h, model) {
				runs[tg.label] = map[bool]flowRun{}
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%t", tg.label, stream), func(t *testing.T) {
						call := runFlowRound(t, h, tg, body, stream)
						requireToolRound(t, tg.label, call.output())

						replay := map[string]any{}
						for key, value := range body {
							replay[key] = value
						}
						input := append([]any{}, body["input"].([]any)...)
						input = append(input, call.output()...)
						input = append(input, toolOutputs(call.output())...)
						replay["input"] = input

						answer := runFlowRound(t, h, tg, replay, stream)
						requireAnswerRound(t, tg.label, answer.response)

						runs[tg.label][stream] = flowRun{call: call, answer: answer}
					})
				}
			}

			reference, wingman := runs["openai"], runs["wingman"]
			for _, stream := range []bool{false, true} {
				r, w := reference[stream], wingman[stream]
				if r.call.response == nil || w.call.response == nil {
					continue
				}
				label := fmt.Sprintf("stream=%t", stream)
				compareItemFields(t, label+"/call", r.call.output(), w.call.output())
				compareItemFields(t, label+"/answer", r.answer.output(), w.answer.output())
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
				compareItemFields(t, label+"/snapshot/call", plain.call.output(), streamed.call.output())
				compareItemFields(t, label+"/snapshot/answer", plain.answer.output(), streamed.answer.output())
			}
		})
	}
}

// TestFlowInstructionsAfterToolOutput: a developer message follows the tool
// result, as coding agents send mid-conversation instructions. Backends whose
// native API takes instructions only at the top must still accept the turn.
func TestFlowInstructionsAfterToolOutput(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store": false,
				"tools": []any{weatherTool},
				"input": []any{
					map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "What's the weather in Bern?"}}},
					map[string]any{"type": "function_call", "id": "fc_flow", "call_id": "call_flow", "name": "get_weather", "arguments": `{"location":"Bern, Switzerland"}`},
					map[string]any{"type": "function_call_output", "call_id": "call_flow", "output": hardtest.ToolResult("get_weather", `{"location":"Bern, Switzerland"}`)},
					map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "Reply in one short sentence, in English."}}},
				},
			}

			rounds := map[string]map[bool]flowRound{}
			for _, tg := range targets(h, model) {
				rounds[tg.label] = map[bool]flowRound{}
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%t", tg.label, stream), func(t *testing.T) {
						round := runFlowRound(t, h, tg, body, stream)
						requireAnswerRound(t, tg.label, round.response)
						rounds[tg.label][stream] = round
					})
				}
			}

			for _, stream := range []bool{false, true} {
				r, w := rounds["openai"][stream], rounds["wingman"][stream]
				if r.response == nil || w.response == nil {
					continue
				}
				compareItemFields(t, fmt.Sprintf("stream=%t", stream), r.output(), w.output())
				if stream {
					compareEventFields(t, "answer", r.events, w.events)
				}
			}
		})
	}
}

// TestFlowPromptCacheAcrossTurns keeps a long instruction prefix stable
// across a tool loop. Backends that cache prompts must report cached input
// tokens on every turn after the first, through Wingman as natively. The
// gateway caches the prefix by default on backends that need markers, so
// an OpenAI-style client gets the same behavior on every backend.
func TestFlowPromptCacheAcrossTurns(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store":            false,
				"tools":            []any{weatherTool, clockTool},
				"instructions":     hardtest.CachePrefix(),
				"prompt_cache_key": "wingman-flow-" + strings.ReplaceAll(model.Name, ".", "-"),
				"input":            userInput(hardtest.ChainedPrompt),
			}

			for _, tg := range targets(h, model) {
				t.Run(tg.label, func(t *testing.T) {
					cached := cachedTokensPerTurn(t, h, tg, body)
					requireCachedTurns(t, tg.label, cached, model.Capabilities.Cache, tg.label == "wingman" && deterministicCache(model.Name))
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
		t.Logf("[%s] turn %d: cached input tokens %v", label, turn+1, tokens)
		if turn == 0 {
			continue
		}
		if tokens > 0 {
			hits++
		} else if expected && deterministic {
			t.Errorf("[%s] turn %d: no cached input tokens although the prefix did not change", label, turn+1)
		}
	}
	if expected && hits == 0 {
		t.Errorf("[%s] no turn reported cached input tokens", label)
	}
}

// cachedTokensPerTurn drives the tool loop and returns each turn's cached
// input tokens.
func cachedTokensPerTurn(t *testing.T, h *openai.Harness, tg target, body map[string]any) []float64 {
	t.Helper()

	input := append([]any{}, body["input"].([]any)...)
	var cached []float64

	for round := 0; round < 6; round++ {
		req := maps.Clone(body)
		req["input"] = input

		resp := post(t, h, tg, req)
		usage, _ := resp["usage"].(map[string]any)
		details, _ := usage["input_tokens_details"].(map[string]any)
		tokens, _ := details["cached_tokens"].(float64)
		cached = append(cached, tokens)

		output, _ := resp["output"].([]any)
		results := toolOutputs(output)
		input = append(input, output...)
		if len(results) == 0 {
			return cached
		}
		input = append(input, results...)
	}

	t.Fatalf("[%s] tool loop did not finish within 6 rounds", tg.label)
	return nil
}

// TestFlowPromptCacheExplicitBreakpoints caches only at the client's
// breakpoint: a developer message ending in prompt_cache_breakpoint, under
// prompt_cache_options explicit. GPT-5.6 and later honor the mode natively,
// Claude backends get the breakpoint as cache_control or a cache point, and
// earlier OpenAI models fall back to implicit caching.
func TestFlowPromptCacheExplicitBreakpoints(t *testing.T) {
	h := openai.New(t)

	for _, model := range openai.DefaultModels() {
		t.Run(model.Name, func(t *testing.T) {
			h.SkipUnlessConfigured(t, model.Name)

			body := map[string]any{
				"store":                false,
				"tools":                []any{weatherTool, clockTool},
				"prompt_cache_options": map[string]any{"mode": "explicit"},
				"input": []any{
					map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": hardtest.CachePrefix(), "prompt_cache_breakpoint": map[string]any{"mode": "explicit"}}}},
					userInput(hardtest.ChainedPrompt)[0],
				},
			}

			for _, tg := range targets(h, model) {
				t.Run(tg.label, func(t *testing.T) {
					if tg.label == "openai" && !explicitCacheModel(tg.model) {
						t.Skipf("reference model %s has no explicit prompt cache mode", tg.model)
					}
					cached := cachedTokensPerTurn(t, h, tg, body)
					requireCachedTurns(t, tg.label, cached, model.Capabilities.Cache, tg.label == "wingman" && deterministicCache(model.Name))
				})
			}
		})
	}
}

// explicitCacheModel reports OpenAI models with explicit prompt cache
// breakpoints (GPT-5.6 and later).
func explicitCacheModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5.6") || strings.HasPrefix(m, "gpt-6")
}
