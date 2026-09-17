package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

func TestCompactionRoundTrip(t *testing.T) {
	for _, api := range []string{"messages", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", api, stream), func(t *testing.T) {
				requests := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if !strings.Contains(r.Header.Get("anthropic-beta"), "compact-2026-09-04") {
						t.Error("missing beta header")
					}
					if body["context_management"] != nil {
						t.Error("on-demand request contains threshold configuration")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"role\":\"assistant\",\"model\":\"claude-test\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n")
					if requests == 1 {
						if body["compaction"].(map[string]any)["type"] != "summarize" {
							t.Error("missing summarize trigger")
						}
						fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"compaction\",\"content\":\"The code is ALPHA-7.\",\"signature\":\"native-signature\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"compaction\"},\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"iterations\":[{\"type\":\"compaction\",\"input_tokens\":42,\"output_tokens\":12}]}}\n\n")
					} else {
						if body["compaction"] != nil {
							t.Error("continuation compacted again")
						}
						messages := body["messages"].([]any)
						if len(messages) != 2 {
							t.Errorf("history was not replaced: %v", messages)
						}
						block := messages[0].(map[string]any)["content"].([]any)[0].(map[string]any)
						if block["signature"] != "native-signature" || block["content"] != "The code is ALPHA-7." || block["encrypted_content"] != nil {
							t.Errorf("corrupt replay: %v", block)
						}
						fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ALPHA-7\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":20,\"output_tokens\":5}}\n\n")
					}
					fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}))
				defer upstream.Close()
				p, _ := anthropic.NewCompleter(upstream.URL, "claude-test", anthropic.WithMaxRetries(0))
				cfg := &config.Config{Policy: noop.New()}
				cfg.RegisterCompleter("claude-test", signatures.ScopedTo("claude-test", p))
				router := chi.NewRouter()
				New(cfg).Attach(router)
				responses.New(cfg).Attach(router)
				post := func(body map[string]any) *httptest.ResponseRecorder {
					data, _ := json.Marshal(body)
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, httptest.NewRequest("POST", "/"+api, bytes.NewReader(data)))
					if rec.Code != http.StatusOK {
						t.Fatalf("request failed: %s", rec.Body.String())
					}
					return rec
				}
				body := map[string]any{"model": "claude-test", "stream": stream}
				if api == "messages" {
					body["max_tokens"] = 4096
					body["messages"] = []any{map[string]any{"role": "user", "content": "Remember ALPHA-7."}}
					body["compaction"] = map[string]any{"type": "summarize"}
				} else {
					body["input"] = []any{map[string]any{"role": "user", "content": "Remember ALPHA-7."}, map[string]any{"type": "compaction_trigger"}}
				}
				rec := post(body)
				var block map[string]any
				if stream {
					events, err := harness.ParseSSE(rec.Body)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if event.Event == "content_block_start" {
							block, _ = event.Data["content_block"].(map[string]any)
						}
						if event.Event == "content_block_delta" {
							t.Fatal("signed compaction must arrive whole, without deltas")
						}
						if event.Event == "response.output_item.done" {
							block, _ = event.Data["item"].(map[string]any)
						}
					}
				} else {
					var result map[string]any
					if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					field := "content"
					if api == "responses" {
						field = "output"
					}
					items := result[field].([]any)
					if len(items) != 1 {
						t.Fatalf("expected summary only: %v", items)
					}
					block = items[0].(map[string]any)
					usage := result["usage"].(map[string]any)
					if usage["input_tokens"] != float64(42) || usage["output_tokens"] != float64(12) {
						t.Fatalf("lost compaction usage: %v", usage)
					}
				}
				if block == nil || block["type"] != "compaction" {
					t.Fatalf("missing compaction: %v", block)
				}
				body["stream"] = false
				if api == "messages" {
					delete(body, "compaction")
					body["messages"] = []any{map[string]any{"role": "assistant", "content": []any{block}}, map[string]any{"role": "user", "content": "Recall the code."}}
				} else {
					body["input"] = []any{block, map[string]any{"role": "user", "content": "Recall the code."}}
				}
				if response := post(body); !strings.Contains(response.Body.String(), "ALPHA-7") {
					t.Fatalf("continuation failed: %s", response.Body.String())
				}
				if requests != 2 {
					t.Fatalf("requests = %d, want 2", requests)
				}
			})
		}
	}
}

func TestCompactionOptionsValidation(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"compaction":{"type":"summarize"}}`, true},
		{`{"compaction":{"type":"other"}}`, false},
		{`{"compaction":{"type":"summarize","instructions":"custom"}}`, false},
		{`{"compaction":{"type":"summarize"},"context_management":{}}`, false},
		{`{"compaction":{"type":"summarize"},"stop_sequences":["STOP"]}`, false},
		{`{"compaction":{"type":"summarize"},"tool_choice":{"type":"any"}}`, false},
		{`{"compaction":{"type":"summarize"},"output_config":{"format":{"type":"json_schema"}}}`, false},
		{`{"context_management":{"edits":[{"type":"compact_20260112","pause_after_compaction":true}]}}`, false},
		{`{"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":49999}}]}}`, false},
		{`{"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":50000}}]}}`, true},
	} {
		var req MessageRequest
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatal(err)
		}
		options, err := toCompleteOptions(req)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.valid && (options.CompactionOptions == nil || options.CompactionOptions.Trigger != (req.Compaction != nil)) {
			t.Fatalf("compaction did not map to the shared options: %+v", options)
		}
	}
}

func TestCompactionWithoutSummaryDoesNotInventContent(t *testing.T) {
	for _, reason := range []provider.StopReason{provider.StopReasonMaxTokens, provider.StopReasonRefusal, provider.StopReasonEndTurn} {
		var events []StreamEvent
		acc := NewStreamingAccumulator("msg_test", "claude-test", func(event StreamEvent) error {
			events = append(events, event)
			return nil
		})
		if err := acc.Add(provider.Completion{Message: &provider.Message{Role: provider.MessageRoleAssistant}, StopReason: reason, Usage: &provider.Usage{InputTokens: 20, OutputTokens: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := acc.Complete(); err != nil {
			t.Fatal(err)
		}
		var stopped bool
		for _, event := range events {
			if event.Type == StreamEventContentBlockStart {
				t.Fatalf("no-summary response invented content: %+v", event.ContentBlock)
			}
			if event.Type == StreamEventMessageDelta {
				stopped = true
				if event.MessageDelta.StopReason != StopReason(reason) {
					t.Fatalf("lost stop reason: %v", event.MessageDelta.StopReason)
				}
			}
		}
		if !stopped {
			t.Fatal("missing stop reason")
		}
	}
}

func TestCompactionTokenLimitReturnsEmptyContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_limit","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":42,"output_tokens":4}]}}

event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer upstream.Close()
	p, _ := anthropic.NewCompleter(upstream.URL, "claude-test", anthropic.WithMaxRetries(0))
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("claude-test", p)
	rec := httptest.NewRecorder()
	New(cfg).handleMessages(rec, httptest.NewRequest("POST", "/messages", strings.NewReader(`{"model":"claude-test","max_tokens":4,"messages":[{"role":"user","content":"Remember ALPHA-7."}],"compaction":{"type":"summarize"}}`)))
	var result Message
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || result.Content == nil || len(result.Content) != 0 || result.StopReason == nil || *result.StopReason != StopReasonMaxTokens || result.Usage.OutputTokens != 4 {
		t.Fatalf("failed compaction should preserve the failure and billed usage without a summary: %s", rec.Body.String())
	}
}
