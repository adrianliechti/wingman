package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
)

// Exercise the SDK decoder, adapter and shared accumulator together, including
// comment-only keepalives (fixed upstream in v1.81.0).
func accumulateStream(t *testing.T, events []string, options *provider.CompleteOptions) (*provider.Completion, error) {
	t.Helper()
	var wire strings.Builder
	for _, event := range events {
		var metadata struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(event), &metadata); err != nil {
			t.Fatal(err)
		}
		wire.WriteString(": keepalive\n\n\n")
		wire.WriteString(sseEvent(metadata.Type, event))
	}
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Request: r, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire.String()))}, nil
	})}
	c, err := NewCompleter("http://anthropic.test", "claude-test", WithClient(client), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	var acc provider.CompletionAccumulator
	for delta, err := range c.Complete(t.Context(), []provider.Message{provider.UserMessage("work")}, options) {
		if err != nil {
			return acc.Result(), err
		}
		acc.Add(*delta)
	}
	return acc.Result(), nil
}

const regressionMessageStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`

func TestStreamToolInputAtBlockStop(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		custom            bool
	}{
		{name: "empty function", input: `{}`, want: `{}`},
		{name: "populated function", input: `{"id":9007199254740993,"nested":{"a.b":[null,false]}}`, want: `{"id":9007199254740993,"nested":{"a.b":[null,false]}}`},
		{name: "empty custom", input: `{}`, custom: true},
		{name: "populated custom", input: `{"input":"print(42)"}`, want: "print(42)", custom: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := &provider.CompleteOptions{}
			if tc.custom {
				options.Tools = []provider.Tool{{Kind: provider.ToolKindCustom, Name: "run"}}
			}
			result, err := accumulateStream(t, []string{
				regressionMessageStart,
				fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"run","input":%s}}`, tc.input),
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
				`{"type":"message_stop"}`,
			}, options)
			if err != nil {
				t.Fatal(err)
			}
			calls := result.Message.ToolCalls()
			if len(calls) != 1 || calls[0].Arguments != tc.want {
				t.Fatalf("tool input: got %+v, want %q", calls, tc.want)
			}
		})
	}
}

func TestStreamRejectsInvalidBlockIndex(t *testing.T) {
	for _, index := range []int64{-1, 0, 9223372036854775807} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			_, err := accumulateStream(t, []string{regressionMessageStart, fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)}, nil)
			if err == nil || !strings.Contains(err.Error(), "index") {
				t.Fatalf("expected invalid block index error, got %v", err)
			}
		})
	}
}

func TestStreamThinkingSignaturesStayWithTheirBlock(t *testing.T) {
	for _, stopBlocks := range []bool{true, false} {
		t.Run(fmt.Sprintf("block_stops=%t", stopBlocks), func(t *testing.T) {
			events := []string{
				regressionMessageStart,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"first ","signature":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thought"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG_"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"1"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"second ","signature":""}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"thought"}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"SIG_"}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"2"}}`,
			}
			if stopBlocks {
				events = append(events, `{"type":"content_block_stop","index":1}`)
			}
			events = append(events, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`, `{"type":"message_stop"}`)
			result, err := accumulateStream(t, events, nil)
			if err != nil {
				t.Fatal(err)
			}
			content := result.Message.Content
			if len(content) != 2 || content[0].Reasoning == nil || content[1].Reasoning == nil {
				t.Fatalf("expected two thinking blocks, got %+v", content)
			}
			for i, want := range []string{"first thought", "second thought"} {
				r := content[i].Reasoning
				if r.ID != "" || r.SummaryText() != "" || r.Text() != want || r.Signature != fmt.Sprintf("SIG_%d", i+1) {
					t.Errorf("block %d: %+v", i, r)
				}
			}
		})
	}
}

func TestToolReplayPreservesJSONNumbers(t *testing.T) {
	const arguments = `{"id":9007199254740993,"nested":{"precise":1.234567890123456789,"huge":1e400,"nil":null}}`
	c, _ := NewCompleter("http://anthropic.test", "claude-test")
	req, err := c.convertMessageRequest([]provider.Message{{Role: provider.MessageRoleAssistant, Content: []provider.Content{
		provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "run", Arguments: arguments}),
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(req.Messages[0].OfAssistant.Content[0].OfToolUse.Input)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]json.RawMessage
	_ = json.Unmarshal(data, &got)
	_ = json.Unmarshal([]byte(arguments), &want)
	for key, value := range want {
		if string(got[key]) != string(value) {
			t.Errorf("%s: got %s, want %s", key, got[key], value)
		}
	}
}
