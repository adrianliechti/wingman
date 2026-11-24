package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/test/harness"
)

// messagesSDKStream mirrors accumulate_event from anthropic-sdk-python
// (src/anthropic/lib/streaming/_messages.py, _beta_messages.py), which backs
// client.messages.stream(). Python appends blocks in arrival order and looks
// deltas up by index; anthropic-sdk-go's Message.Accumulate rejects a start
// index that is not the next block and appends signature deltas where Python
// replaces them. A stream rejected here breaks or silently corrupts one of them.
type messagesSDKStream struct {
	started, delta, stopped bool
	stopReason              string

	content []map[string]any
	open    map[int]bool
	input   map[int]string // input_json_delta buffers
	signed  map[int]bool
}

// Each delta type only lands on these block types; Python drops the rest.
var messagesSDKDeltaTargets = map[string][]string{
	"text_delta":       {"text"},
	"citations_delta":  {"text"},
	"input_json_delta": {"tool_use", "server_tool_use"},
	"thinking_delta":   {"thinking"},
	"signature_delta":  {"thinking"},
	"compaction_delta": {"compaction"},
}

func (s *messagesSDKStream) handle(event map[string]any) error {
	kind, _ := event["type"].(string)
	if kind == "ping" {
		return nil
	}
	if s.stopped {
		return fmt.Errorf("%s after the stream ended", kind)
	}
	if !s.started {
		if kind != "message_start" {
			return fmt.Errorf(`Unexpected event order, got %s before "message_start"`, kind)
		}
		message, _ := event["message"].(map[string]any)
		if content, _ := message["content"].([]any); len(content) != 0 {
			return errors.New("message_start carries content blocks")
		}
		s.started = true
		s.open, s.input, s.signed = map[int]bool{}, map[int]string{}, map[int]bool{}
		return nil
	}
	index := -1
	if f, ok := event["index"].(float64); ok {
		index = int(f)
	}
	block := func() (map[string]any, error) {
		if index < 0 || index >= len(s.content) {
			return nil, fmt.Errorf("IndexError: %s for block %d with %d blocks", kind, index, len(s.content))
		}
		if !s.open[index] {
			return nil, fmt.Errorf("%s for block %d, which is not open", kind, index)
		}
		return s.content[index], nil
	}
	switch kind {
	case "message_start":
		return errors.New("second message_start")
	case "content_block_start":
		if s.delta {
			return errors.New("content_block_start after message_delta")
		}
		if index != len(s.content) {
			return fmt.Errorf("received event of type content_block_start for content block at index %d, expected index %d", index, len(s.content))
		}
		start, _ := event["content_block"].(map[string]any)
		s.content = append(s.content, messagesClone(start))
		s.open[index] = true
	case "content_block_delta":
		b, err := block()
		if err != nil {
			return err
		}
		delta, _ := event["delta"].(map[string]any)
		deltaType, _ := delta["type"].(string)
		targets, known := messagesSDKDeltaTargets[deltaType]
		if !known {
			return fmt.Errorf("unknown delta type %q", deltaType)
		}
		if !slices.Contains(targets, b["type"].(string)) {
			return fmt.Errorf("SDK drops %s on a %v block", deltaType, b["type"])
		}
		switch deltaType {
		case "text_delta":
			b["text"] = messagesString(b["text"]) + messagesString(delta["text"])
		case "thinking_delta":
			b["thinking"] = messagesString(b["thinking"]) + messagesString(delta["thinking"])
		case "signature_delta":
			if s.signed[index] {
				return errors.New("second signature_delta: anthropic-sdk-go appends it, anthropic-sdk-python replaces it")
			}
			s.signed[index] = true
			b["signature"] = delta["signature"]
		case "input_json_delta":
			s.input[index] += messagesString(delta["partial_json"])
			if !messagesJSONPrefix(s.input[index]) {
				return fmt.Errorf("Unable to parse tool parameter JSON from model. JSON: %s", s.input[index])
			}
		case "compaction_delta":
			b["content"], b["encrypted_content"] = delta["content"], delta["encrypted_content"]
		case "citations_delta":
			citations, _ := b["citations"].([]any)
			b["citations"] = append(citations, delta["citation"])
		}
	case "content_block_stop":
		b, err := block()
		if err != nil {
			return err
		}
		delete(s.open, index)
		if buf := s.input[index]; buf != "" {
			var input any
			if err := json.Unmarshal([]byte(buf), &input); err != nil {
				return fmt.Errorf("tool input is not complete JSON at content_block_stop: %s", buf)
			}
			b["input"] = input
		}
	case "message_delta":
		if s.delta {
			return errors.New("second message_delta")
		}
		if len(s.open) != 0 {
			return fmt.Errorf("message_delta while blocks %v are open", s.open)
		}
		usage, _ := event["usage"].(map[string]any)
		if _, ok := usage["output_tokens"].(float64); !ok {
			return errors.New("message_delta without usage.output_tokens")
		}
		delta, _ := event["delta"].(map[string]any)
		if s.stopReason, _ = delta["stop_reason"].(string); s.stopReason == "" {
			return errors.New("message_delta without stop_reason")
		}
		s.delta = true
	case "message_stop":
		if !s.delta {
			return errors.New("message_stop before message_delta")
		}
		s.stopped = true
	case "error":
		s.stopped = true
	default:
		return fmt.Errorf("unknown event %q", kind)
	}
	return nil
}

// messagesJSONPrefix reports whether buf could still become valid JSON, which
// is what jiter's partial mode accepts.
func messagesJSONPrefix(buf string) bool {
	decoder := jsontext.NewDecoder(strings.NewReader(buf))
	for {
		_, err := decoder.ReadToken()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return errors.Is(err, io.ErrUnexpectedEOF)
		}
	}
}

func messagesString(v any) string {
	s, _ := v.(string)
	return s
}

func messagesClone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}

// checkMessagesSDKStream replays a /messages SSE body through the SDK model
// and returns the message a client reconstructs.
func checkMessagesSDKStream(t *testing.T, body string) (content []map[string]any, stopReason string) {
	t.Helper()
	events, err := harness.ParseSSE(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var state messagesSDKStream
	for _, event := range events {
		if event.Event != event.Data["type"] {
			t.Fatalf("SSE name %q differs from payload type: %s", event.Event, event.Raw)
		}
		if err := state.handle(event.Data); err != nil {
			t.Fatalf("anthropic SDK rejects %s: %v\nstream:\n%s", event.Raw, err, body)
		}
	}
	if !state.stopped || !state.delta {
		t.Fatalf("stream did not finish:\n%s", body)
	}
	return state.content, state.stopReason
}

func TestMessagesSDKStreamRejectsKnownViolations(t *testing.T) {
	const (
		start    = `{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`
		thinking = `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`
		tool     = `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"n","input":{}}}`
		stop0    = `{"type":"content_block_stop","index":0}`
		delta    = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`
	)
	signature := `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s"}}`
	inputDelta := func(partial string) string {
		return fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%q}}`, partial)
	}
	for _, tc := range []struct {
		name   string
		events []string
		want   string
	}{
		{"missing message_start", []string{thinking}, "before \"message_start\""},
		{"index gap", []string{start, `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`}, "expected index 0"},
		{"delta for unknown block", []string{start, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`}, "IndexError"},
		{"text on thinking block", []string{start, thinking, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`}, "SDK drops text_delta"},
		{"second signature", []string{start, thinking, signature, signature}, "second signature_delta"},
		{"delta after stop", []string{start, thinking, stop0, signature}, "not open"},
		{"invalid partial json", []string{start, tool, inputDelta(`{"a"}`)}, "Unable to parse tool parameter JSON"},
		{"truncated json at stop", []string{start, tool, inputDelta(`{"a":`), stop0}, "not complete JSON"},
		{"message_delta with open block", []string{start, thinking, delta}, "are open"},
		{"message_stop without delta", []string{start, `{"type":"message_stop"}`}, "before message_delta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state messagesSDKStream
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
	for prefix, want := range map[string]bool{`{"a":`: true, `{"a":"x`: true, `{"a":[1,`: true, `{"a"}`: false, `{"a":1}}`: false} {
		if got := messagesJSONPrefix(prefix); got != want {
			t.Errorf("messagesJSONPrefix(%q)=%t, want %t", prefix, got, want)
		}
	}
}

// messagesShape renders blocks as type:id=state=text for comparison.
// Thinking signatures carry the OpenAI item id and its ciphertext.
func messagesShape(content []map[string]any) []string {
	var shape []string
	for _, block := range content {
		entry, _ := block["type"].(string)
		switch entry {
		case "thinking":
			if signature := messagesString(block["signature"]); signature != "" {
				id, ciphertext := provider.UnwrapReasoningID(signature)
				entry += ":" + id + "=" + strings.TrimPrefix(ciphertext, "@target:")
			}
			if text := messagesString(block["thinking"]); text != "" {
				entry += "=" + text
			}
		case "text":
			entry += "=" + messagesString(block["text"])
		case "tool_use":
			input, _ := json.Marshal(block["input"], json.Deterministic(true))
			entry += ":" + messagesString(block["id"]) + "=" + string(input)
		}
		shape = append(shape, entry)
	}
	return shape
}

func messagesRequest(stream bool, messages ...any) map[string]any {
	if len(messages) == 0 {
		messages = []any{map[string]any{"role": "user", "content": "check"}}
	}
	return map[string]any{
		"model": "target", "max_tokens": 1000, "stream": stream,
		"thinking": map[string]any{"type": "enabled", "budget_tokens": 2000},
		"tools":    []any{map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object"}}},
		"messages": messages,
	}
}

// Claude Code and other Messages clients reach OpenAI models through this
// handler. Every stream must be one the Anthropic SDKs accumulate, its blocks
// must keep the upstream item order, and the reconstructed message must
// replay as valid Responses input.
func TestMessagesStreamFromResponsesSDKCompatible(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream func() string
		stop     string
		stream   []string
		json     []string // when different from stream
	}{
		// Simple: one item type per stream; the terminal snapshot equals
		// the done events.
		{"text", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Message("msg_1", "final_answer", "Hel", "lo")
			return u.Finish("completed", nil)
		}, "end_turn", []string{"text=Hello"}, nil},
		{"reasoning then text", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1", "Thinking it over")
			u.Message("msg_1", "final_answer", "Done.")
			return u.Finish("completed", nil)
		}, "end_turn", []string{"thinking:rs_1=enc-1=Thinking it over", "text=Done."}, nil},
		{"reasoning then tool call", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1", "Need data")
			u.Call("fc_1", "call_1", "lookup", `{"q":`, `"x"}`)
			return u.Finish("completed", nil)
		}, "tool_use", []string{"thinking:rs_1=enc-1=Need data", `tool_use:call_1={"q":"x"}`}, nil},
		{"commentary then tool call", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1")
			u.Message("msg_1", "commentary", "Checking.")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", nil)
		}, "tool_use", []string{"thinking:rs_1=enc-1", "text=Checking.", "tool_use:call_1={}"}, nil},

		// Hard: the terminal snapshot replays, refreshes or is the only
		// source of state for items whose blocks already streamed.
		{"interleaved reasoning refreshed by snapshot", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1a", "Plan")
			u.Message("msg_1", "commentary", "Looking.")
			u.Reasoning("rs_2", "enc-2a", "Pick tool")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", func(output []any) {
				harness.SetEncrypted("rs_1", "enc-1b")(output)
				harness.SetEncrypted("rs_2", "enc-2b")(output)
			})
		}, "tool_use",
			// A closed block keeps the ciphertext it streamed; both decrypt
			// to the same reasoning.
			[]string{"thinking:rs_1=enc-1a=Plan", "text=Looking.", "thinking:rs_2=enc-2a=Pick tool", "tool_use:call_1={}"},
			[]string{"thinking:rs_1=enc-1b=Plan", "text=Looking.", "thinking:rs_2=enc-2b=Pick tool", "tool_use:call_1={}"}},
		{"back-to-back reasoning items", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1", "First")
			u.Reasoning("rs_2", "enc-2", "Second")
			u.Message("msg_1", "final_answer", "Ok")
			return u.Finish("completed", harness.SetEncrypted("rs_1", "enc-1b"))
		}, "end_turn",
			[]string{"thinking:rs_1=enc-1=First", "thinking:rs_2=enc-2=Second", "text=Ok"},
			[]string{"thinking:rs_1=enc-1b=First", "thinking:rs_2=enc-2=Second", "text=Ok"}},
		{"parallel tool calls after reasoning", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1", "Two lookups")
			u.Call("fc_1", "call_1", "lookup", `{"q":`, `1}`)
			u.Call("fc_2", "call_2", "lookup", `{"q":2}`)
			return u.Finish("completed", nil)
		}, "tool_use", []string{"thinking:rs_1=enc-1=Two lookups", `tool_use:call_1={"q":1}`, `tool_use:call_2={"q":2}`}, nil},
		{"ciphertext only in snapshot for closed items", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "")
			u.Call("fc_1", "call_1", "lookup", `{"a":1}`)
			u.Reasoning("rs_2", "")
			u.Call("fc_2", "call_2", "lookup", `{"b":2}`)
			return u.Finish("completed", func(output []any) {
				harness.SetEncrypted("rs_1", "enc-1")(output)
				harness.SetEncrypted("rs_2", "enc-2")(output)
				output[0].(map[string]any)["summary"] = []any{map[string]any{"type": "summary_text", "text": "Plan"}}
			})
		}, "tool_use",
			[]string{"thinking:rs_1=enc-1=Plan", `tool_use:call_1={"a":1}`, "thinking:rs_2=enc-2", `tool_use:call_2={"b":2}`}, nil},
		{"ciphertext only in snapshot for summarized item", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "", "Plan")
			u.Message("msg_1", "final_answer", "Answer")
			return u.Finish("completed", harness.SetEncrypted("rs_1", "enc-1"))
		}, "end_turn", []string{"thinking:rs_1=enc-1=Plan", "text=Answer"}, nil},
		{"summary only in snapshot after item closed", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", func(output []any) {
				output[0].(map[string]any)["summary"] = []any{map[string]any{"type": "summary_text", "text": "Late"}}
			})
		}, "tool_use", []string{"thinking:rs_1=enc-1", "tool_use:call_1={}"}, []string{"thinking:rs_1=enc-1=Late", "tool_use:call_1={}"}},
		{"no item done events", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.OmitItemDone = true
			u.Reasoning("rs_1", "enc-1", "Plan")
			u.Message("msg_1", "commentary", "Looking.")
			u.Reasoning("rs_2", "enc-2")
			u.Call("fc_1", "call_1", "lookup", `{}`)
			return u.Finish("completed", nil)
		}, "tool_use",
			[]string{"thinking:rs_1=enc-1=Plan", "text=Looking.", "thinking:rs_2=enc-2", "tool_use:call_1={}"}, nil},
		{"snapshot without item events", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Output = []any{
				map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "enc-1", "status": "completed"},
				map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Hi", "annotations": []any{}}}},
			}
			return u.Finish("completed", nil)
		}, "end_turn", []string{"thinking:rs_1=enc-1", "text=Hi"}, nil},
		{"incomplete after reasoning and text", func() string {
			u := harness.NewResponsesStream("gpt-test")
			u.Reasoning("rs_1", "enc-1", "Long answer")
			u.Message("msg_1", "final_answer", "Part")
			return u.Finish("incomplete", harness.SetEncrypted("rs_1", "enc-1b"))
		}, "max_tokens", []string{"thinking:rs_1=enc-1=Long answer", "text=Part"}, []string{"thinking:rs_1=enc-1b=Long answer", "text=Part"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []map[string]any
			wire := tc.upstream()
			c, err := openai.NewResponder("https://api.openai.com", "gpt-test", openai.WithToken("test"), openai.WithClient(featureClient(t, wire, func(_ *http.Request, sent map[string]any) {
				requests = append(requests, sent)
			})))
			if err != nil {
				t.Fatal(err)
			}
			router := featureRouter(c)

			rec := featurePost(t, router, "/messages", messagesRequest(true))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
			}
			streamed, stop := checkMessagesSDKStream(t, rec.Body.String())
			if got := messagesShape(streamed); !slices.Equal(got, tc.stream) || stop != tc.stop {
				t.Fatalf("streamed message:\n got %q stop=%s\nwant %q stop=%s", got, stop, tc.stream, tc.stop)
			}

			rec = featurePost(t, router, "/messages", messagesRequest(false))
			var message struct {
				StopReason string           `json:"stop_reason"`
				Content    []map[string]any `json:"content"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s (%v)", rec.Code, rec.Body, err)
			}
			want := tc.json
			if want == nil {
				want = tc.stream
			}
			if got := messagesShape(message.Content); !slices.Equal(got, want) || message.StopReason != tc.stop {
				t.Fatalf("JSON message:\n got %q stop=%s\nwant %q stop=%s", got, message.StopReason, want, tc.stop)
			}

			// Send the streamed message back, as a client continuing the
			// conversation does, and inspect what reaches OpenAI.
			assistant := map[string]any{"role": "assistant", "content": streamed}
			var results []any
			for _, block := range streamed {
				if block["type"] == "tool_use" {
					results = append(results, map[string]any{"type": "tool_result", "tool_use_id": block["id"], "content": "42"})
				}
			}
			next := map[string]any{"role": "user", "content": append(results, map[string]any{"type": "text", "text": "continue"})}
			rec = featurePost(t, router, "/messages", messagesRequest(false, map[string]any{"role": "user", "content": "check"}, assistant, next))
			if rec.Code != http.StatusOK {
				t.Fatalf("replay HTTP %d: %s", rec.Code, rec.Body)
			}
			input := requests[len(requests)-1]["input"].([]any)
			replayed := map[string]bool{}
			for i, raw := range input {
				item := raw.(map[string]any)
				if item["type"] != "reasoning" {
					continue
				}
				id, _ := item["id"].(string)
				if replayed[id] {
					t.Errorf("replay repeats reasoning item %q", id)
				}
				replayed[id] = true
				// The Responses API pairs reasoning with the item after it.
				if i+1 >= len(input) {
					t.Fatalf("reasoning %q replays as the last input item: %v", id, input)
				}
				following := input[i+1].(map[string]any)
				if following["type"] != "function_call" && following["role"] != "assistant" && following["type"] != "reasoning" {
					t.Fatalf("reasoning %q replays before %v instead of its output: %v", id, following, input)
				}
			}
			for _, block := range streamed {
				if signature := messagesString(block["signature"]); block["type"] == "thinking" && signature != "" {
					if id, _ := provider.UnwrapReasoningID(signature); !replayed[id] {
						t.Errorf("signed thinking block %q was not replayed: %v", id, input)
					}
				}
			}
		})
	}
}

// A Claude backend behind this handler: signed and redacted thinking, text,
// and a tool call whose input arrives in fragments.
func TestMessagesStreamFromClaudeSDKCompatible(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_up","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":20,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ahead"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-a"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Checking."}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
		`{"type":"message_stop"}`,
	}
	var wire strings.Builder
	for _, event := range events {
		var typed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(event), &typed); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&wire, "event: %s\ndata: %s\n\n", typed.Type, event)
	}
	p, err := anthropic.NewCompleter("https://upstream.invalid", "claude-opus-5", anthropic.WithClient(featureClient(t, wire.String(), func(*http.Request, map[string]any) {})))
	if err != nil {
		t.Fatal(err)
	}
	router := featureRouter(p)
	rec := featurePost(t, router, "/messages", messagesRequest(true))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	streamed, stop := checkMessagesSDKStream(t, rec.Body.String())
	rec = featurePost(t, router, "/messages", messagesRequest(false))
	var message struct {
		StopReason string           `json:"stop_reason"`
		Content    []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s (%v)", rec.Code, rec.Body, err)
	}
	want := []string{"thinking:=sig-a=Plan ahead", "redacted_thinking", "text=Checking.", `tool_use:toolu_1={"q":"x"}`}
	if got := messagesShape(streamed); !slices.Equal(got, want) || stop != "tool_use" {
		t.Fatalf("streamed message:\n got %q stop=%s\nwant %q", got, stop, want)
	}
	if got := messagesShape(message.Content); !slices.Equal(got, want) || message.StopReason != "tool_use" {
		t.Fatalf("JSON message:\n got %q stop=%s\nwant %q", got, message.StopReason, want)
	}
	for i, block := range streamed {
		if block["type"] == "redacted_thinking" && block["data"] != message.Content[i]["data"] {
			t.Fatalf("redacted data differs: streamed=%v JSON=%v", block["data"], message.Content[i]["data"])
		}
	}
}

// The same SDK model applied to each backend fixture the feature tests use.
func TestMessagesStreamSDKCompatibleAcrossBackends(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	for _, backend := range []string{"claude", "claude-fallback", "responses", "chat", "gemini", "xai", "bedrock"} {
		t.Run(backend, func(t *testing.T) {
			p := featureBackend(t, backend, func(*http.Request, map[string]any) {})
			rec := featurePost(t, featureRouter(p), "/messages", map[string]any{"model": "target", "max_tokens": 1000, "stream": true,
				"messages": []any{map[string]any{"role": "user", "content": "hi"}}})
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
			}
			content, _ := checkMessagesSDKStream(t, rec.Body.String())
			if shape := messagesShape(content); !slices.Contains(shape, "text=done") {
				t.Fatalf("streamed message lost the answer: %q", shape)
			}
		})
	}
}
