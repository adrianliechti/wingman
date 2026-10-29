package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/test/harness"
	claude "github.com/anthropics/anthropic-sdk-go/v2"
)

// These fixtures exercise the Messages wire contract without depending on the
// backend that produced the completion chunks.
type messagesContractCompleter struct {
	chunks []provider.Completion
	err    error
}

func (c messagesContractCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		for _, chunk := range c.chunks {
			if !yield(&chunk, nil) {
				return
			}
		}
		if c.err != nil {
			yield(nil, c.err)
		}
	}
}

func messagesContractChunk(content ...provider.Content) provider.Completion {
	return provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: content}}
}

func messagesContractRequest(t *testing.T, chunks []provider.Completion, err error, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("target", messagesContractCompleter{chunks: chunks, err: err})
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"model":"target","max_tokens":1024,"stream":%t,"messages":[{"role":"user","content":"Continue"}]}`, stream)
	New(cfg).handleMessages(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(body)))
	return rec
}

func TestMessagesStreamContract(t *testing.T) {
	text := func(id, value string) provider.Completion {
		return messagesContractChunk(provider.Content{MessageID: id, Text: value})
	}
	thinking := func(id, summary, signature string) provider.Completion {
		return messagesContractChunk(provider.ReasoningContent(provider.Reasoning{ID: id, Summary: provider.TextParts(summary), Signature: signature}))
	}
	tool := func(id, name, args string) provider.Completion {
		return messagesContractChunk(provider.ToolCallContent(provider.ToolCall{ID: id, Name: name, Arguments: args}))
	}
	tests := []struct {
		name   string
		chunks []provider.Completion
		kinds  []string
		stop   StopReason
	}{
		{"empty", nil, nil, StopReasonEndTurn},
		{"metadata only", []provider.Completion{{Model: "target"}, thinking("rs_empty", "", "")}, nil, StopReasonEndTurn},
		{"explicit empty text", []provider.Completion{messagesContractChunk(provider.Content{TextPresent: true})}, []string{"text"}, StopReasonEndTurn},
		{"fragmented text", []provider.Completion{text("msg_1", "Hel"), text("msg_1", "lo")}, []string{"text"}, StopReasonEndTurn},
		{"distinct messages", []provider.Completion{text("msg_1", "First"), text("msg_2", "Second")}, []string{"text", "text"}, StopReasonEndTurn},
		{"summary and signature", []provider.Completion{thinking("rs_1", "Weigh", ""), thinking("rs_1", " options", ""), thinking("rs_1", "", "opaque"), text("msg_1", "Answer")}, []string{"thinking", "text"}, StopReasonEndTurn},
		{"unsigned summary", []provider.Completion{thinking("rs_1", "Summary", ""), text("msg_1", "Answer")}, []string{"thinking", "text"}, StopReasonEndTurn},
		{"signature only", []provider.Completion{thinking("rs_1", "", "opaque"), text("msg_1", "Answer")}, []string{"thinking", "text"}, StopReasonEndTurn},
		{"redacted thinking", []provider.Completion{messagesContractChunk(provider.ReasoningContent(provider.Reasoning{Redacted: true, Signature: "redacted"})), text("msg_1", "Answer")}, []string{"redacted_thinking", "text"}, StopReasonEndTurn},
		{"two reasoning items", []provider.Completion{thinking("rs_1", "First", "sig_1"), thinking("rs_2", "Second", "sig_2"), text("msg_1", "Answer")}, []string{"thinking", "thinking", "text"}, StopReasonEndTurn},
		{"fragmented tool", []provider.Completion{tool("call_1", "lookup", `{"city":`), tool("call_1", "", `"Bern"}`)}, []string{"tool_use"}, StopReasonToolUse},
		{"interleaved tools", []provider.Completion{tool("call_1", "lookup", `{"city":`), tool("call_2", "clock", `{"zone":`), tool("call_1", "", `"Bern"}`), tool("call_2", "", `"UTC"}`)}, []string{"tool_use", "tool_use"}, StopReasonToolUse},
		{"zero argument tool", []provider.Completion{tool("call_1", "ping", "")}, []string{"tool_use"}, StopReasonToolUse},
		{"reasoning and tool in one chunk", []provider.Completion{messagesContractChunk(provider.ReasoningContent(provider.Reasoning{ID: "rs_1", Summary: provider.TextParts("Plan"), Signature: "sig_1"}), provider.ToolCallContent(provider.ToolCall{ID: "call_1", Name: "ping", Arguments: `{}`}))}, []string{"thinking", "tool_use"}, StopReasonToolUse},
		{"caller tool named like hosted tool", []provider.Completion{tool("call_1", "tool_search_tool_regex", `{"pattern":"x"}`)}, []string{"tool_use"}, StopReasonToolUse},
		{"empty token limit", []provider.Completion{{Status: provider.CompletionStatusIncomplete}}, nil, StopReasonMaxTokens},
		{"truncated tool", []provider.Completion{tool("call_1", "lookup", `{"city":`), {Status: provider.CompletionStatusIncomplete}}, []string{"tool_use"}, StopReasonMaxTokens},
		{"context limit overrides tool", []provider.Completion{tool("call_1", "ping", `{}`), {Status: provider.CompletionStatusIncomplete, StopReason: provider.StopReasonContextExceeded}}, []string{"tool_use"}, StopReasonModelContextWindowExceeded},
		{"native pause", []provider.Completion{text("msg_1", "Working"), {StopReason: provider.StopReasonPauseTurn}}, []string{"text"}, StopReasonPauseTurn},
		{"stop sequence", []provider.Completion{text("msg_1", "Done"), {StopReason: provider.StopReasonStopSequence, StopSequence: "END"}}, []string{"text"}, StopReasonStopSequence},
		{"refusal", []provider.Completion{messagesContractChunk(provider.Content{Refusal: "Cannot help"}), {Status: provider.CompletionStatusRefused, StopDetails: &provider.StopDetails{Type: "refusal", Category: "cyber", Explanation: "Declined"}}}, []string{"text"}, StopReasonRefusal},
		{"late usage", []provider.Completion{text("msg_1", "Answer"), {Usage: &provider.Usage{InputTokens: 100, OutputTokens: 15, CacheReadInputTokens: 30, CacheCreationInputTokens: 20, ReasoningTokens: new(5)}}}, []string{"text"}, StopReasonEndTurn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := messagesContractRequest(t, tt.chunks, nil, true)
			if stream.Code != http.StatusOK || stream.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatalf("stream response: %d %s", stream.Code, stream.Body)
			}
			events, err := harness.ParseSSE(stream.Body)
			if err != nil {
				t.Fatal(err)
			}
			var message claude.Message
			open := map[int64]string{}
			var kinds []string
			starts, deltas, stops := 0, 0, 0
			for i, raw := range events {
				var event claude.MessageStreamEventUnion
				if err := json.Unmarshal([]byte(raw.Raw), &event); err != nil {
					t.Fatal(err)
				}
				if raw.Event != event.Type {
					t.Fatalf("SSE name %q differs from type %q", raw.Event, event.Type)
				}
				switch event.Type {
				case "message_start":
					starts++
					if i != 0 || len(event.Message.Content) != 0 || event.Message.StopReason != "" {
						t.Fatalf("invalid message_start: %s", raw.Raw)
					}
				case "content_block_start":
					if event.Index != int64(len(kinds)) {
						t.Fatalf("block index %d after %d starts", event.Index, len(kinds))
					}
					open[event.Index] = event.ContentBlock.Type
					kinds = append(kinds, event.ContentBlock.Type)
				case "content_block_delta":
					want := map[string]string{"text_delta": "text", "thinking_delta": "thinking", "signature_delta": "thinking", "input_json_delta": "tool_use"}[event.Delta.Type]
					if want == "" || open[event.Index] != want {
						t.Fatalf("delta outside matching open block: %s", raw.Raw)
					}
				case "content_block_stop":
					if open[event.Index] == "" {
						t.Fatalf("stop outside open block: %s", raw.Raw)
					}
					delete(open, event.Index)
				case "message_delta":
					deltas++
					if len(open) != 0 || i != len(events)-2 {
						t.Fatalf("message_delta before blocks closed: %s", raw.Raw)
					}
				case "message_stop":
					stops++
					if i != len(events)-1 {
						t.Fatal("events after message_stop")
					}
				default:
					t.Fatalf("unexpected event %q", event.Type)
				}
				if err := message.Accumulate(event); err != nil {
					t.Fatalf("SDK accumulate %s: %v", raw.Raw, err)
				}
			}
			if starts != 1 || deltas != 1 || stops != 1 || !reflect.DeepEqual(kinds, tt.kinds) || StopReason(message.StopReason) != tt.stop {
				t.Fatalf("invalid lifecycle: starts=%d deltas=%d stops=%d kinds=%v stop=%q", starts, deltas, stops, kinds, message.StopReason)
			}
			// A real SDK's reconstructed message must agree with the JSON response.
			complete := messagesContractRequest(t, tt.chunks, nil, false)
			if complete.Code != http.StatusOK {
				t.Fatalf("JSON response: %d %s", complete.Code, complete.Body)
			}
			var expected claude.Message
			if err := json.Unmarshal(complete.Body.Bytes(), &expected); err != nil {
				t.Fatal(err)
			}
			if message.StopReason != expected.StopReason || message.StopSequence != expected.StopSequence {
				t.Fatalf("stream stop reason/sequence differs from JSON: streamed=%q/%q JSON=%q/%q", message.StopReason, message.StopSequence, expected.StopReason, expected.StopSequence)
			}
			if got, want := message.StopDetails, expected.StopDetails; got.Type != want.Type || got.Category != want.Category || got.Explanation != want.Explanation {
				t.Fatalf("stream stop details differ from JSON: streamed=%+v JSON=%+v", got, want)
			}
			if got, want := message.Usage, expected.Usage; got.InputTokens != want.InputTokens || got.OutputTokens != want.OutputTokens || got.CacheReadInputTokens != want.CacheReadInputTokens || got.CacheCreationInputTokens != want.CacheCreationInputTokens || got.OutputTokensDetails.ThinkingTokens != want.OutputTokensDetails.ThinkingTokens {
				t.Fatalf("stream usage differs from JSON: streamed=%+v JSON=%+v", got, want)
			}
			if len(message.Content) != len(expected.Content) {
				t.Fatalf("stream content differs from JSON: streamed=%+v JSON=%+v", message.Content, expected.Content)
			}
			for i, block := range message.Content {
				want := expected.Content[i]
				if block.Type != want.Type || block.Text != want.Text || block.Thinking != want.Thinking || block.Signature != want.Signature || block.Data != want.Data || block.ID != want.ID || block.Name != want.Name || !reflect.DeepEqual(block.Input, want.Input) {
					t.Fatalf("block %d differs: streamed=%+v JSON=%+v", i, block, want)
				}
			}
		})
	}
}

func TestMessagesFailures(t *testing.T) {
	for _, visible := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, tc := range []struct {
				name   string
				failed bool
				err    error
				status int
				kind   string
			}{
				{"reported failure", true, nil, 502, "api_error"},
				{"transport error", false, errors.New("connection reset"), 502, "api_error"},
				{"rate limit", false, &provider.ProviderError{Code: 429, Message: "Slow down", RetryAfter: 2 * time.Second}, 429, "rate_limit_error"},
				{"invalid request", false, provider.InvalidRequest(errors.New("Unsupported option")), 400, "invalid_request_error"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/visible=%t", tc.name, stream, visible), func(t *testing.T) {
					var chunks []provider.Completion
					if visible {
						chunks = append(chunks, messagesContractChunk(provider.TextContent("Partial response")))
					}
					if tc.failed {
						chunks = append(chunks, provider.Completion{Status: provider.CompletionStatusFailed, StopDetails: &provider.StopDetails{Type: "upstream_failed"}})
					}
					rec := messagesContractRequest(t, chunks, tc.err, stream)
					if tc.status == 429 {
						if stream && visible {
							if !strings.Contains(rec.Body.String(), "retry: 2000\n") {
								t.Fatal("stream lost retry delay")
							}
						} else if rec.Header().Get("Retry-After") != "2" {
							t.Fatal("error lost Retry-After header")
						}
					}
					if stream && (visible || tc.failed) {
						if rec.Code != 200 {
							t.Fatalf("started stream changed HTTP status: %d", rec.Code)
						}
						events, err := harness.ParseSSE(rec.Body)
						if err != nil {
							t.Fatal(err)
						}
						errorCount := 0
						for i, event := range events {
							if event.Event == "message_delta" || event.Event == "message_stop" {
								t.Fatalf("failure emitted success event: %s", event.Raw)
							}
							if event.Event == "error" {
								errorCount++
								var response ErrorResponse
								if err := json.Unmarshal([]byte(event.Raw), &response); err != nil || response.Error.Type != tc.kind || i != len(events)-1 {
									t.Fatalf("wrong terminal error: %s (%v)", event.Raw, err)
								}
							}
						}
						if errorCount != 1 {
							t.Fatalf("got %d error events", errorCount)
						}
					} else {
						var response ErrorResponse
						if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != tc.status || response.Type != "error" || response.Error.Type != tc.kind {
							t.Fatalf("wrong error: status=%d body=%s (%v)", rec.Code, rec.Body, err)
						}
					}
				})
			}
		}
	}
}

func TestMessagesFailedToolsAreNotFinalized(t *testing.T) {
	for _, call := range []provider.ToolCall{
		{ID: "call_1", Name: "lookup", Arguments: `{"city":`},
		{ID: "call_1", Name: "ping"},
		{ID: "call_1", Name: "apply_patch", Kind: provider.ToolKindTextEditor, Arguments: `{"type":"create_file","path":"hello.txt",`},
	} {
		t.Run(call.Name, func(t *testing.T) {
			rec := messagesContractRequest(t, []provider.Completion{
				messagesContractChunk(provider.ToolCallContent(call)),
				{Status: provider.CompletionStatusFailed},
			}, nil, true)
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) == 0 || events[len(events)-1].Event != "error" {
				t.Fatalf("failed tool did not terminate with an error: %+v", events)
			}
			for _, event := range events {
				if event.Event == "content_block_stop" || event.Event == "message_stop" {
					t.Fatalf("failed tool was finalized: %s", event.Raw)
				}
				if call.Kind == provider.ToolKindTextEditor && event.Event == "content_block_delta" {
					t.Fatalf("failure fabricated translated tool arguments: %s", event.Raw)
				}
			}
		})
	}
}

func TestMessagesStreamPreservesLateCacheBreakdown(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		t.Run(fmt.Sprintf("earlier_usage=%t", earlier), func(t *testing.T) {
			chunks := []provider.Completion{messagesContractChunk(provider.TextContent("Answer"))}
			if earlier {
				chunks[0].Usage = &provider.Usage{InputTokens: 100, CacheCreationInputTokens: 20, CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{StandardTokens: 20}}
			}
			chunks = append(chunks, provider.Completion{Usage: &provider.Usage{
				InputTokens: 100, OutputTokens: 12, CacheCreationInputTokens: 50,
				CacheWriteTokensDetails: &provider.CacheWriteTokensDetails{StandardTokens: 20, ExtendedTokens: 30},
			}})
			rec := messagesContractRequest(t, chunks, nil, true)
			events, err := harness.ParseSSE(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Event != "message_delta" {
					continue
				}
				var delta struct {
					Usage Usage `json:"usage"`
				}
				if err := json.Unmarshal([]byte(event.Raw), &delta); err != nil {
					t.Fatal(err)
				}
				want := &CacheCreation{Ephemeral5mInputTokens: 20, Ephemeral1hInputTokens: 30}
				if delta.Usage.InputTokens != 50 || delta.Usage.CacheCreationInputTokens != 50 || !reflect.DeepEqual(delta.Usage.CacheCreation, want) {
					t.Fatalf("late usage lost cache breakdown: %+v", delta.Usage)
				}
				return
			}
			t.Fatal("missing message_delta")
		})
	}
}
