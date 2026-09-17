package features_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

// CLAUDE_RELEASE_LIVE=1 go test ./test/anthropic/features -run TestClaudeReleaseE2E -v
// Uses local Wingman handlers and the real Claude API, including signed replay.
func TestClaudeReleaseE2E(t *testing.T) {
	if os.Getenv("CLAUDE_RELEASE_LIVE") != "1" {
		t.Skip("set CLAUDE_RELEASE_LIVE=1 for paid Claude release tests")
	}
	h := anthropic.New(t)
	h.Client.Timeout = 180 * time.Second
	const model = "claude-opus-5"
	p, err := claude.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, claude.WithToken(h.Anthropic.APIKey), claude.WithClient(h.Client.HTTP), claude.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
	router := chi.NewRouter()
	server.New(cfg).Attach(router)
	responses.New(cfg).Attach(router)
	local := httptest.NewServer(router)
	defer local.Close()
	h.Wingman = harness.Endpoint{Name: "wingman", BaseURL: local.URL}
	post := func(t *testing.T, body map[string]any) map[string]any {
		t.Helper()
		result := anthropic.PostMessages(t, h, h.Wingman, body)
		if result.StatusCode != 200 {
			t.Fatalf("status %d: %s", result.StatusCode, result.RawBody)
		}
		return result.Body
	}

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("instruction_lifetime/stream=%t", stream), func(t *testing.T) {
			history := []any{
				map[string]any{"role": "user", "content": "What color is a clear daytime sky?"},
				map[string]any{"role": "system", "clear_at": "next_user_message", "content": "Answer color questions in German, with a single lowercase color word."},
			}
			body := map[string]any{"model": model, "max_tokens": 4096, "stream": stream,
				"system": "Answer questions in English using a single lowercase color word.", "messages": history,
				"output_config": map[string]any{"effort": "low"},
			}
			for _, expected := range []string{"blau", "green"} {
				output := postLiveOutput(t, h.Client, h.Wingman, "messages", body)
				var answer strings.Builder
				for _, raw := range output {
					block := raw.(map[string]any)
					if block["type"] == "text" {
						answer.WriteString(block["text"].(string))
					}
				}
				if strings.TrimSpace(answer.String()) != expected {
					var types []any
					for _, raw := range output {
						types = append(types, raw.(map[string]any)["type"])
					}
					t.Fatalf("instruction lifetime: got %q, want %q (blocks: %v)", answer.String(), expected, types)
				}
				body["messages"] = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": "What color is healthy grass? Please answer in English."})
			}
		})
	}

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("default_thinking/stream=%t", stream), func(t *testing.T) {
			body := map[string]any{"model": model, "max_tokens": 4096, "stream": stream, "messages": []any{
				map[string]any{"role": "user", "content": "Find the smallest positive integer x such that x mod 7 = 3, x mod 11 = 5, and x mod 13 = 7. Verify the remainders; give a brief answer."},
			}}
			var blocks []any
			var usage map[string]any
			if stream {
				events := anthropic.PostMessagesSSE(t, h, h.Wingman, body)
				for _, event := range events {
					switch event.Event {
					case "error":
						t.Fatalf("stream error: %v", event.Data)
					case "content_block_start":
						blocks = append(blocks, event.Data["content_block"])
					case "content_block_delta":
						block := blocks[int(event.Data["index"].(float64))].(map[string]any)
						delta := event.Data["delta"].(map[string]any)
						for _, field := range []string{"thinking", "signature", "text"} {
							if value, ok := delta[field].(string); ok {
								previous, _ := block[field].(string)
								block[field] = previous + value
							}
						}
					case "message_delta":
						usage, _ = event.Data["usage"].(map[string]any)
					}
				}
			} else {
				result := post(t, body)
				blocks, _ = result["content"].([]any)
				usage, _ = result["usage"].(map[string]any)
			}
			var signed bool
			for _, raw := range blocks {
				block := raw.(map[string]any)
				if block["type"] == "thinking" {
					if _, ok := block["thinking"].(string); !ok {
						t.Fatalf("missing thinking string: %v", block)
					}
					if signature, _ := block["signature"].(string); signature != "" {
						signed = true
					}
				}
			}
			if !signed {
				t.Fatal("model did not return replayable default thinking")
			}
			details, _ := usage["output_tokens_details"].(map[string]any)
			if tokens, _ := details["thinking_tokens"].(float64); tokens <= 0 {
				t.Fatalf("missing default thinking usage: %v", usage)
			}
			body["stream"] = false
			body["messages"] = append(body["messages"].([]any), map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": "What value did you find? Reply with only the integer."})
			result := post(t, body)
			data, _ := json.Marshal(result["content"])
			if !strings.Contains(string(data), "423") {
				t.Fatalf("replay lost the answer: %s", data)
			}
		})
	}

	t.Run("effort_and_strict_tool", func(t *testing.T) {
		body := map[string]any{
			"model": model, "max_tokens": 4096,
			"output_config": map[string]any{"effort": "high"},
			"messages": []any{
				map[string]any{"role": "user", "content": "Call echo with value RELEASE-7."},
				map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "low"}},
			},
			"tools": []any{map[string]any{"name": "echo", "description": "Echo the given value", "strict": true, "input_schema": map[string]any{
				"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false,
			}}},
		}
		result := post(t, body)
		var call map[string]any
		for _, raw := range result["content"].([]any) {
			block := raw.(map[string]any)
			if block["type"] == "tool_use" {
				call = block
			}
		}
		if call == nil || call["name"] != "echo" || call["input"].(map[string]any)["value"] != "RELEASE-7" {
			t.Fatalf("expected strict echo call: %v", call)
		}
		body["messages"] = append(body["messages"].([]any), map[string]any{"role": "assistant", "content": result["content"]}, map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": call["id"], "content": "RELEASE-7"},
		}}, map[string]any{"role": "system", "content": "Reply with only the value returned by echo; make no more tool calls.", "output_config": map[string]any{"effort": "medium"}})
		result = post(t, body)
		if result["stop_reason"] != "end_turn" {
			t.Fatalf("tool loop did not finish: %v", result["stop_reason"])
		}
		data, _ := json.Marshal(result["content"])
		if !strings.Contains(string(data), "RELEASE-7") {
			t.Fatalf("tool result was lost: %s", data)
		}
	})
}
