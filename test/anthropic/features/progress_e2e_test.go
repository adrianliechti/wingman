package features_test

import (
	"encoding/json/v2"
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
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/harness"

	"github.com/go-chi/chi/v5"
)

// PROGRESS_NOTES_LIVE=1 go test ./test/anthropic/features -run TestProgressNotesLive -v
// Compare signed tool loops through both Wingman APIs with the same Claude
// model directly. Progress notes are optional, so their exact count may vary.
// Set TEST_PROGRESS_BEDROCK_MODEL=eu.anthropic.claude-sonnet-5 to include Bedrock.
func TestProgressNotesLive(t *testing.T) {
	if os.Getenv("PROGRESS_NOTES_LIVE") != "1" {
		t.Skip("set PROGRESS_NOTES_LIVE=1 for paid progress-note comparisons")
	}
	h := anthropic.New(t)
	h.Client.Timeout = 120 * time.Second
	const model = "claude-sonnet-5-5"
	p, err := claude.NewCompleter(strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), model, claude.WithToken(h.Anthropic.APIKey), claude.WithClient(h.Client.HTTP), claude.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, p))
	bedrockModel := os.Getenv("TEST_PROGRESS_BEDROCK_MODEL")
	if bedrockModel != "" {
		p, err := bedrock.NewCompleter(bedrockModel)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegisterCompleter(bedrockModel, signatures.ScopedTo(bedrockModel, p))
	}
	router := chi.NewRouter()
	server.New(cfg).Attach(router)
	responses.New(cfg).Attach(router)
	local := httptest.NewServer(router)
	defer local.Close()
	wingman := harness.Endpoint{Name: "wingman", BaseURL: local.URL}
	type target struct {
		endpoint harness.Endpoint
		api      string
		model    string
	}
	targets := []target{{h.Anthropic, "messages", model}, {wingman, "messages", model}, {wingman, "responses", model}}
	if bedrockModel != "" {
		for _, api := range []string{"messages", "responses"} {
			targets = append(targets, target{wingman, api, bedrockModel})
		}
	}

	for _, mode := range []string{"updates", "between_tools"} {
		for _, stream := range []bool{false, true} {
			for _, target := range targets {
				t.Run(fmt.Sprintf("%s/stream=%t/%s/%s/%s", mode, stream, target.endpoint.Name, target.api, target.model), func(t *testing.T) {
					history := []any{map[string]any{"role": "user", "content": "Call lookup with code ALPHA-7. If its result has next_code, call lookup again with that code. Give a brief progress update between the calls. Once you receive a status, reply with only that status value."}}
					schema := map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}}, "required": []string{"code"}, "additionalProperties": false}
					body := map[string]any{"model": target.model, "stream": stream}
					if target.api == "messages" {
						body["messages"], body["max_tokens"] = history, 2048
						body["thinking"] = map[string]any{"type": "adaptive", "display": "updates"}
						body["output_config"] = map[string]any{"effort": "high"}
						if mode == "between_tools" {
							body["thinking"] = map[string]any{"type": "between_tools"}
						}
						body["tools"] = []any{map[string]any{"name": "lookup", "description": "Look up a code's status", "input_schema": schema}}
					} else {
						body["input"], body["max_output_tokens"] = history, 2048
						effort := "high"
						if mode == "between_tools" {
							effort = "none"
						}
						body["reasoning"] = map[string]any{"effort": effort}
						body["include"] = []string{"reasoning.encrypted_content"}
						body["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "description": "Look up a code's status", "parameters": schema}}
					}
					for turn, toolResult := range []string{`{"next_code":"BETA-8"}`, `{"status":"READY-7"}`} {
						output := postLiveOutput(t, h.Client, target.endpoint, target.api, body)
						var callID string
						var signed, notes int
						for _, raw := range output {
							block := raw.(map[string]any)
							switch block["type"] {
							case "thinking", "reasoning":
								key := "signature"
								if target.api == "responses" {
									key = "encrypted_content"
									if parts, _ := block["summary"].([]any); len(parts) > 0 {
										notes++
									}
									if parts, _ := block["content"].([]any); len(parts) > 0 {
										t.Fatal("updates exposed reasoning content instead of a progress summary")
									}
								} else if text, _ := block["thinking"].(string); text != "" {
									notes++
								}
								if signature, _ := block[key].(string); signature == "" {
									t.Fatal("thinking block lost its signature")
								}
								signed++
							case "tool_use", "function_call":
								if block["name"] != "lookup" || callID != "" {
									t.Fatalf("unexpected tool: %v", block["name"])
								}
								key := "id"
								input, _ := block["input"].(map[string]any)
								if target.api == "responses" {
									key = "call_id"
									arguments, _ := block["arguments"].(string)
									if err := json.Unmarshal([]byte(arguments), &input); err != nil {
										t.Fatal(err)
									}
								}
								if input["code"] != []string{"ALPHA-7", "BETA-8"}[turn] {
									t.Fatalf("lost lookup code: %v", input)
								}
								callID, _ = block[key].(string)
							}
						}
						if callID == "" {
							t.Fatal("model did not call lookup")
						}
						t.Logf("signed blocks=%d, visible progress notes=%d", signed, notes)
						if target.api == "messages" {
							history = append(history, map[string]any{"role": "assistant", "content": output}, map[string]any{"role": "user", "content": []any{
								map[string]any{"type": "tool_result", "tool_use_id": callID, "content": toolResult},
							}})
							body["messages"] = history
						} else {
							history = append(append(history, output...), map[string]any{"type": "function_call_output", "call_id": callID, "output": toolResult})
							body["input"] = history
						}
					}
					result := postLiveOutput(t, h.Client, target.endpoint, target.api, body)
					var answer strings.Builder
					for _, raw := range result {
						block := raw.(map[string]any)
						if block["type"] == "tool_use" || block["type"] == "function_call" {
							t.Fatal("model requested another tool after receiving the status")
						}
						if block["type"] == "text" {
							answer.WriteString(block["text"].(string))
						}
						if block["type"] == "message" {
							for _, part := range block["content"].([]any) {
								content := part.(map[string]any)
								if content["type"] == "output_text" {
									answer.WriteString(content["text"].(string))
								}
							}
						}
					}
					if strings.TrimSpace(answer.String()) != "READY-7" {
						t.Fatalf("tool replay lost the result: %q", answer.String())
					}
				})
			}
		}
	}
}
