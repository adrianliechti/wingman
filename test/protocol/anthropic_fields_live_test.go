package protocol_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	// PROTOCOL_REFERENCE_LIVE=1 go test ./test/protocol -run TestLiveAnthropicFields -v
	// Compare every JSON field against the SAME upstream generation, retaining raw
	// objects so an SDK's typed decoder cannot silently discard newly added fields.
	// Only gateway message IDs and the reversible signature scope are normalized.
	// SSE chunk boundaries and ping timing are transport-specific; their contents,
	// block indexes, lifecycle, start message and final message are checked.
	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
)

func TestLiveAnthropicFields(t *testing.T) {
	referenceLive(t, "ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_FIELDS_MODEL")
	if model == "" {
		model = liveAnthropicModel
	}
	capture := &nativeCapture{}
	backend, err := anthropic.NewCompleter("https://api.anthropic.com", model,
		anthropic.WithToken(os.Getenv("ANTHROPIC_API_KEY")), anthropic.WithMaxRetries(0),
		anthropic.WithClient(&http.Client{Transport: capture, Timeout: 120 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, backend))
	local := configuredGateway(t, cfg)
	local.Client().Timeout = 120 * time.Second

	for _, stream := range []bool{false, true} {
		cachePrefix := fmt.Sprintf("Live field parity %d %t.\n", time.Now().UnixNano(), stream) + strings.Repeat("Keep this reference text unchanged. The answer is OK.\n", 400)
		for _, scenario := range []string{"text", "tool", "structured", "max_tokens", "stop_sequence", "thinking", "cache_write", "cache_read"} {
			t.Run(fmt.Sprintf("%s/stream=%t", scenario, stream), func(t *testing.T) {
				body := map[string]any{"model": model, "max_tokens": 2048, "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "Reply with exactly: Grüezi reference 42"}},
				}
				switch scenario {
				case "tool":
					body["messages"] = []any{map[string]any{"role": "user", "content": `Call record with city "Zürich" and value 9007199254740993.`}}
					body["tools"] = []any{map[string]any{"name": "record", "input_schema": map[string]any{
						"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}, "value": map[string]any{"type": "integer"}},
						"required": []string{"city", "value"}, "additionalProperties": false,
					}}}
					body["tool_choice"] = map[string]any{"type": "auto"}
				case "structured":
					body["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": map[string]any{
						"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}, "additionalProperties": false,
					}}}
				case "max_tokens":
					body["max_tokens"] = 1
				case "stop_sequence":
					body["stop_sequences"] = []string{"reference"}
				case "thinking":
					body["messages"] = []any{map[string]any{"role": "user", "content": "Find the smallest positive integer x with x mod 7 = 3, x mod 11 = 5, and x mod 13 = 7. Verify your answer."}}
					body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
					// At low effort Claude 5.5 can answer without any thinking;
					// exercise signed blocks and replay instead of vacuous parity.
					body["output_config"] = map[string]any{"effort": "high"}
				case "cache_write", "cache_read":
					body["system"] = cachePrefix
					body["cache_control"] = map[string]any{"type": "ephemeral", "ttl": "1h"}
				}
				capture.reset()
				status, headers, raw := post(t, local, "/v1/messages", body)
				native := capture.only(t)
				saveReferenceFields(t, map[string]any{"request": body, "upstream_request": jsontext.Value(native.requestBody), "native": string(native.body), "wingman": string(raw)})
				requireStatus(t, native.status, http.StatusOK, native.body)
				requireStatus(t, status, native.status, raw)
				compareAnthropicRequest(t, body, native.requestBody)
				want, wantStart, wantDelta, wantBlocks := anthropicFieldMessage(t, native.body)
				var got map[string]any
				if stream {
					requireType(t, headers, "text/event-stream")
					var gotStart, gotDelta map[string]any
					var gotBlocks []any
					got, gotStart, gotDelta, gotBlocks = anthropicFieldMessage(t, raw)
					compareAnthropicFields(t, "message_start", wantStart, gotStart)
					compareExactFields(t, "message_delta", wantDelta, gotDelta)
					compareExactFields(t, "content_block_start", wantBlocks, gotBlocks)
				} else {
					requireType(t, headers, "application/json")
					got = exactObject(t, raw)
				}
				compareAnthropicFields(t, "message", want, got)
				switch scenario {
				case "tool":
					equal(t, "stop_reason", got["stop_reason"], "tool_use")
					var calls []map[string]any
					for _, raw := range got["content"].([]any) {
						block := raw.(map[string]any)
						if block["type"] == "tool_use" {
							calls = append(calls, block)
						}
					}
					if len(calls) != 1 {
						t.Fatalf("expected one tool call, got %d", len(calls))
					}
					equal(t, "tool name", calls[0]["name"], "record")
					compareExactFields(t, "tool input", map[string]any{"city": "Zürich", "value": jsonutil.Number("9007199254740993")}, calls[0]["input"])
				case "structured":
					output := exactObject(t, []byte(anthropicFieldText(got)))
					requireFieldKeys(t, output, "answer")
					equal(t, "structured answer", output["answer"], "Grüezi reference 42")
				case "max_tokens":
					equal(t, "stop_reason", got["stop_reason"], "max_tokens")
				case "stop_sequence":
					equal(t, "stop_reason", got["stop_reason"], "stop_sequence")
				case "thinking":
					if at(got, "content", 0, "type") != "thinking" || textValue(at(got, "content", 0, "signature")) == "" {
						t.Error("missing signed thinking block")
					}
					t.Run("replay", func(t *testing.T) {
						history := append(slices.Clone(body["messages"].([]any)), map[string]any{"role": "assistant", "content": got["content"]}, map[string]any{"role": "user", "content": "What integer did you find? Reply only with the integer."})
						followup := maps.Clone(body)
						followup["messages"], followup["stream"] = history, false
						capture.reset()
						status, _, raw := post(t, local, "/v1/messages", followup)
						native := capture.only(t)
						saveReferenceFields(t, map[string]any{"request": followup, "upstream_request": jsontext.Value(native.requestBody), "native": string(native.body), "wingman": string(raw)})
						requireStatus(t, status, http.StatusOK, raw)
						requireStatus(t, native.status, http.StatusOK, native.body)
						upstream := exactObject(t, native.requestBody)
						compareExactFields(t, "replayed signed blocks", want["content"], at(upstream, "messages", 1, "content"))
						want, _, _, _ := anthropicFieldMessage(t, native.body)
						got := exactObject(t, raw)
						compareAnthropicFields(t, "message", want, got)
						equal(t, "replayed answer", strings.TrimSpace(anthropicFieldText(got)), "423")
					})
				case "cache_write":
					if number(at(got, "usage", "cache_creation", "ephemeral_1h_input_tokens")) <= 0 {
						t.Error("top-level 1h cache control did not create extended cache entries")
					}
				case "cache_read":
					if number(at(got, "usage", "cache_read_input_tokens")) <= 0 {
						t.Error("identical prefix did not hit the cache")
					}
				}
			})
		}
	}
}

// The generic map decoder preserves nulls, absent fields and integer precision.
// Unknown event/delta types fail rather than silently disappearing.
func anthropicFieldMessage(t *testing.T, raw []byte) (map[string]any, map[string]any, map[string]any, []any) {
	t.Helper()
	var message, start, terminal map[string]any
	var blocks []any
	var blockStarts []any
	open := map[int]bool{}
	arguments := map[int]string{}
	done, deltaSeen := false, false
	for _, event := range frames(t, raw) {
		data := exactObject(t, []byte(event.Raw))
		if data["type"] != event.Event || done {
			t.Fatalf("invalid SSE event order/type: %s", event.Raw)
		}
		switch event.Event {
		case "ping":
			requireFieldKeys(t, data, "type")
		case "message_start":
			requireFieldKeys(t, data, "type", "message")
			if message != nil {
				t.Fatal("duplicate message_start")
			}
			start = data["message"].(map[string]any)
			encoded, _ := json.Marshal(start)
			message = exactObject(t, encoded)
			if len(message["content"].([]any)) != 0 {
				t.Fatal("message_start content must be empty")
			}
		case "content_block_start":
			requireFieldKeys(t, data, "type", "index", "content_block")
			index := number(data["index"])
			if message == nil || deltaSeen || index != len(blocks) {
				t.Fatalf("invalid block start: %s", event.Raw)
			}
			blocks = append(blocks, data["content_block"])
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			blockStarts = append(blockStarts, exactObject(t, encoded))
			open[index] = true
		case "content_block_delta":
			requireFieldKeys(t, data, "type", "index", "delta")
			index := number(data["index"])
			if !open[index] {
				t.Fatalf("delta outside an open block: %s", event.Raw)
			}
			block := blocks[index].(map[string]any)
			delta := data["delta"].(map[string]any)
			switch delta["type"] {
			case "text_delta", "thinking_delta", "signature_delta":
				field := strings.TrimSuffix(delta["type"].(string), "_delta")
				requireFieldKeys(t, delta, "type", field)
				kind := "thinking"
				if field == "text" {
					kind = "text"
				}
				equal(t, "delta block kind", block["type"], kind)
				block[field] = textValue(block[field]) + textValue(delta[field])
			case "input_json_delta":
				requireFieldKeys(t, delta, "type", "partial_json")
				equal(t, "delta block kind", block["type"], "tool_use")
				arguments[index] += delta["partial_json"].(string)
			default:
				t.Fatalf("unhandled delta: %s", event.Raw)
			}
		case "content_block_stop":
			requireFieldKeys(t, data, "type", "index")
			index := number(data["index"])
			if !open[index] {
				t.Fatalf("stop outside an open block: %s", event.Raw)
			}
			delete(open, index)
			if partial := arguments[index]; partial != "" {
				blocks[index].(map[string]any)["input"] = exactObject(t, []byte(partial))
			}
		case "message_delta":
			requireFieldKeys(t, data, "type", "delta", "usage")
			if message == nil || len(open) != 0 || deltaSeen {
				t.Fatal("message_delta before blocks closed or duplicate delta")
			}
			maps.Copy(message, data["delta"].(map[string]any))
			maps.Copy(message["usage"].(map[string]any), data["usage"].(map[string]any))
			terminal = data
			deltaSeen = true
		case "message_stop":
			requireFieldKeys(t, data, "type")
			if !deltaSeen || len(open) != 0 {
				t.Fatal("message_stop without a completed message")
			}
			done = true
		default:
			t.Fatalf("unhandled event: %s", event.Raw)
		}
	}
	if !done {
		t.Fatal("missing message_stop")
	}
	if blocks == nil {
		blocks = []any{}
	}
	message["content"] = blocks
	return message, start, terminal, blockStarts
}

func TestLiveAnthropicErrorFields(t *testing.T) {
	referenceLive(t, "ANTHROPIC_API_KEY")
	for _, scenario := range []string{"unknown_model", "invalid_schema", "excessive_max_tokens"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", scenario, stream), func(t *testing.T) {
				model := liveAnthropicModel
				if scenario == "unknown_model" {
					model = "claude-missing-field-parity-model"
				}
				capture := &nativeCapture{}
				backend, err := anthropic.NewCompleter("https://api.anthropic.com", model,
					anthropic.WithToken(os.Getenv("ANTHROPIC_API_KEY")), anthropic.WithMaxRetries(0),
					anthropic.WithClient(&http.Client{Transport: capture, Timeout: 30 * time.Second}))
				if err != nil {
					t.Fatal(err)
				}
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter(model, backend)
				local := configuredGateway(t, cfg)
				body := map[string]any{"model": model, "max_tokens": 32, "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "Reply OK"}},
				}
				if scenario == "invalid_schema" {
					body["tools"] = []any{map[string]any{"name": "record", "input_schema": map[string]any{
						"type": "object", "properties": map[string]any{"value": map[string]any{"type": "invalid_schema_type"}},
					}}}
				}
				if scenario == "excessive_max_tokens" {
					body["max_tokens"] = 10000000
				}
				status, headers, raw := post(t, local, "/v1/messages", body)
				native := capture.only(t)
				saveReferenceFields(t, map[string]any{"request": body, "upstream_request": jsontext.Value(native.requestBody), "native": string(native.body), "wingman": string(raw)})
				if native.status < 400 {
					t.Fatalf("invalid request unexpectedly succeeded: HTTP %d", native.status)
				}
				requireStatus(t, status, native.status, raw)
				requireType(t, headers, "application/json")
				compareExactFields(t, "error", exactObject(t, native.body), exactObject(t, raw))
				if native.requestID == "" || headers.Get("Request-Id") != native.requestID {
					t.Error("upstream request ID header not preserved")
				}
			})
		}
	}
}

func requireFieldKeys(t *testing.T, object map[string]any, keys ...string) {
	t.Helper()
	got := slices.Sorted(maps.Keys(object))
	slices.Sort(keys)
	if !slices.Equal(got, keys) {
		t.Fatalf("field set differs: got %v, want %v", got, keys)
	}
}

func compareAnthropicFields(t *testing.T, label string, want, got map[string]any) {
	t.Helper()
	// Compare copies: the original gateway signatures must survive for replay.
	want = cloneFieldObject(t, want)
	got = cloneFieldObject(t, got)
	for _, message := range []map[string]any{want, got} {
		if id, ok := message["id"].(string); !ok || !strings.HasPrefix(id, "msg_") || len(id) <= 4 {
			t.Fatalf("invalid message id: %v", message["id"])
		}
		message["id"] = "msg_<generated>"
	}
	blocks, _ := got["content"].([]any)
	for _, raw := range blocks {
		block := raw.(map[string]any)
		if signature, ok := block["signature"].(string); ok && signature != "" {
			// Decode independently of production ParseSignature, checking the realm
			// as well as the exact native payload rather than discarding any prefix.
			value, scoped := strings.CutPrefix(signature, "@"+textValue(want["model"])+":")
			if !scoped {
				t.Error("thinking signature has a missing or incorrect model scope")
				continue
			}
			block["signature"] = value
		}
	}
	compareExactFields(t, label, want, got)
}

func cloneFieldObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return exactObject(t, encoded)
}

func anthropicFieldText(message map[string]any) string {
	var text strings.Builder
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		if at(raw, "type") == "text" {
			text.WriteString(textValue(at(raw, "text")))
		}
	}
	return text.String()
}

// The gateway intentionally streams upstream and adds default caching. Explicit
// caller controls must nevertheless reach the provider without being discarded.
func compareAnthropicRequest(t *testing.T, body map[string]any, wire []byte) {
	t.Helper()
	want, got := cloneFieldObject(t, body), exactObject(t, wire)
	equal(t, "upstream stream", got["stream"], true)
	for _, field := range []string{"model", "max_tokens", "stop_sequences", "thinking", "output_config", "cache_control", "tool_choice"} {
		if value, present := want[field]; present {
			compareExactFields(t, "request."+field, value, got[field])
		}
	}
	compareExactFields(t, "request.user_text", at(want, "messages", 0, "content"), at(got, "messages", 0, "content", 0, "text"))
	if system, present := want["system"]; present {
		compareExactFields(t, "request.system", system, at(got, "system", 0, "text"))
	}
	if tools, present := want["tools"].([]any); present {
		actual, _ := got["tools"].([]any)
		if len(actual) != len(tools) {
			t.Fatalf("request.tools: native count %d, requested %d", len(actual), len(tools))
		}
		for i, tool := range tools {
			for field, value := range tool.(map[string]any) {
				compareExactFields(t, fmt.Sprintf("request.tools[%d].%s", i, field), value, at(actual[i], field))
			}
		}
	}
}

func compareExactFields(t *testing.T, path string, want, got any) {
	t.Helper()
	if expected, ok := want.(map[string]any); ok {
		actual, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: expected object, got %T", path, got)
			return
		}
		for _, key := range slices.Sorted(maps.Keys(expected)) {
			if value, present := actual[key]; !present {
				t.Errorf("%s.%s: missing field (native=%v)", path, key, expected[key])
			} else {
				compareExactFields(t, path+"."+key, expected[key], value)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(actual)) {
			if _, present := expected[key]; !present {
				t.Errorf("%s.%s: unexpected field (wingman=%v)", path, key, actual[key])
			}
		}
		return
	}
	if expected, ok := want.([]any); ok {
		actual, ok := got.([]any)
		if !ok || len(expected) != len(actual) {
			t.Errorf("%s: array shape differs: native=%d wingman=%d (%T)", path, len(expected), len(actual), got)
			return
		}
		for i := range expected {
			compareExactFields(t, fmt.Sprintf("%s[%d]", path, i), expected[i], actual[i])
		}
		return
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: native=%v (%T), wingman=%v (%T)", path, want, want, got, got)
	}
}

func saveReferenceFields(t *testing.T, evidence any) {
	t.Helper()
	dir := os.Getenv("PROTOCOL_REFERENCE_OUTPUT")
	if dir == "" {
		dir = os.Getenv("ANTHROPIC_FIELDS_OUTPUT")
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(evidence, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	name := strings.NewReplacer("/", "_", "=", "-").Replace(t.Name()) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), bytes.TrimSpace(data), 0600); err != nil {
		t.Fatal(err)
	}
}
