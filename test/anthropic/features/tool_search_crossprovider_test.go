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
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

// TOOL_SEARCH_LIVE=1 go test ./test/anthropic/features -run TestToolSearchCrossProviderLive -v
// Runs Wingman's handlers locally against real providers. Each source discovers
// a deferred tool, then its complete output is replayed with the tool result to
// both the original model and other models/providers, over HTTP and SSE.
func TestToolSearchCrossProviderLive(t *testing.T) {
	if os.Getenv("TOOL_SEARCH_LIVE") != "1" {
		t.Skip("set TOOL_SEARCH_LIVE=1 for paid tool-search tests")
	}
	h := anthropic.New(t)
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Fatal("OPENAI_API_KEY is required for cross-provider tests")
	}
	h.Client.Timeout = 180 * time.Second
	models := []string{"claude-opus-5", "claude-fable-5-1", "gpt-5.4"}
	cfg := &config.Config{Policy: noop.New()}
	for _, model := range models {
		if strings.HasPrefix(model, "claude") {
			p, err := claude.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, claude.WithToken(h.Anthropic.APIKey), claude.WithClient(h.Client.HTTP), claude.WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
		} else {
			base := os.Getenv("OPENAI_BASE_URL")
			if base == "" {
				base = "https://api.openai.com/v1"
			}
			p, err := openai.NewResponder(base, model, openai.WithToken(os.Getenv("OPENAI_API_KEY")), openai.WithClient(h.Client.HTTP), openai.WithMaxRetries(0))
			if err != nil {
				t.Fatal(err)
			}
			cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
		}
	}
	router := chi.NewRouter()
	server.New(cfg).Attach(router)
	responses.New(cfg).Attach(router)
	local := httptest.NewServer(router)
	defer local.Close()
	endpoint := harness.Endpoint{Name: "wingman", BaseURL: local.URL}

	for _, apiCase := range []string{"messages", "responses", "responses-namespaced"} {
		api := strings.TrimSuffix(apiCase, "-namespaced")
		for _, stream := range []bool{false, true} {
			for _, source := range models {
				t.Run(fmt.Sprintf("%s/stream=%t/from=%s", apiCase, stream, source), func(t *testing.T) {
					body := map[string]any{"model": source, "stream": stream}
					history := []any{map[string]any{"role": "user", "content": "Find lookup_project using tool search, then call it with project ALPHA-7. After its result, reply with only the status value."}}
					schema := map[string]any{"type": "object", "properties": map[string]any{"project": map[string]any{"type": "string"}}, "required": []string{"project"}, "additionalProperties": false}
					if api == "messages" {
						body["messages"], body["max_tokens"] = history, 4096
						body["output_config"] = map[string]any{"effort": "low"}
						body["tools"] = []any{map[string]any{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"}, map[string]any{"name": "lookup_project", "description": "Look up a project's current status by project code.", "input_schema": schema, "defer_loading": true}}
					} else {
						body["input"], body["max_output_tokens"] = history, 4096
						body["reasoning"] = map[string]any{"effort": "low", "summary": "auto"}
						body["include"] = []string{"reasoning.encrypted_content"}
						body["tools"] = []any{map[string]any{"type": "tool_search"}, map[string]any{"type": "function", "name": "lookup_project", "description": "Look up a project's current status by project code.", "parameters": schema, "defer_loading": true}}
						if apiCase == "responses-namespaced" {
							tool := body["tools"].([]any)[1]
							body["tools"].([]any)[1] = map[string]any{"type": "namespace", "name": "projects", "description": "Tools for looking up project status.", "tools": []any{tool}}
						}
					}
					output := postLiveOutput(t, h.Client, endpoint, api, body)
					var callID string
					var searched, loaded bool
					for _, item := range output {
						block := item.(map[string]any)
						switch block["type"] {
						case "server_tool_use", "tool_search_call":
							searched = true
						case "tool_search_tool_result", "tool_search_output":
							loaded = true
						case "tool_use", "function_call":
							if block["name"] == "lookup_project" {
								callID, _ = block["call_id"].(string)
								if api == "messages" {
									callID, _ = block["id"].(string)
								}
							}
						}
					}
					if !searched || !loaded || callID == "" {
						t.Fatalf("expected search, loaded definitions and lookup_project call; got search=%t loaded=%t call=%q", searched, loaded, callID)
					}
					if api == "messages" {
						body["messages"] = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": callID, "content": `{"status":"READY-7"}`}}})
					} else {
						body["input"] = append(append(history, output...), map[string]any{"type": "function_call_output", "call_id": callID, "output": `{"status":"READY-7"}`})
					}
					for _, target := range models {
						t.Run("to="+target, func(t *testing.T) {
							body["model"] = target
							result := postLiveOutput(t, h.Client, endpoint, api, body)
							var answer strings.Builder
							for _, item := range result {
								block := item.(map[string]any)
								if block["type"] == "tool_use" || block["type"] == "function_call" {
									t.Fatal("model requested another tool instead of completing the loop")
								}
								if text, ok := block["text"].(string); ok {
									answer.WriteString(text)
								}
								if content, ok := block["content"].([]any); ok {
									for _, part := range content {
										if text, ok := part.(map[string]any)["text"].(string); ok {
											answer.WriteString(text)
										}
									}
								}
							}
							if strings.TrimSpace(answer.String()) != "READY-7" {
								t.Fatalf("lost tool result: %q", answer.String())
							}
						})
					}
				})
			}
		}
	}
}

func postLiveOutput(t *testing.T, client *harness.Client, endpoint harness.Endpoint, api string, body map[string]any) []any {
	t.Helper()
	if stream, _ := body["stream"].(bool); !stream {
		response, err := client.Post(t.Context(), endpoint, "/"+api, body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("HTTP %d: %s", response.StatusCode, response.RawBody)
		}
		key := "output"
		if api == "messages" {
			key = "content"
		}
		output, _ := response.Body[key].([]any)
		return output
	}
	events, err := client.PostSSE(t.Context(), endpoint, "/"+api, body)
	if err != nil {
		t.Fatal(err)
	}
	var output []any
	arguments := map[int]string{}
	for _, event := range events {
		switch event.Event {
		case "error", "response.failed":
			t.Fatalf("stream error: %v", event.Data)
		case "response.completed":
			response, _ := event.Data["response"].(map[string]any)
			output, _ = response["output"].([]any)
		case "content_block_start":
			output = append(output, event.Data["content_block"])
		case "content_block_delta":
			i := int(event.Data["index"].(float64))
			block := output[i].(map[string]any)
			delta := event.Data["delta"].(map[string]any)
			for _, field := range []string{"thinking", "signature", "text"} {
				if value, ok := delta[field].(string); ok {
					previous, _ := block[field].(string)
					block[field] = previous + value
				}
			}
			if value, ok := delta["partial_json"].(string); ok {
				arguments[i] += value
			}
		}
	}
	for i, args := range arguments {
		var input any
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			t.Fatal(err)
		}
		output[i].(map[string]any)["input"] = input
	}
	if len(output) == 0 {
		t.Fatal("stream returned no completed output")
	}
	return output
}
