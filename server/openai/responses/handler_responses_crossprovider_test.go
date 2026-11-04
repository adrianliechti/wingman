package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	"github.com/adrianliechti/wingman/pkg/provider/anthropic"
	"github.com/adrianliechti/wingman/pkg/provider/bedrock"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
)

// Paid opt-in test. The four mixed cycles cover all twelve directed
// transitions between Google, OpenAI, Anthropic and Bedrock, with full history.
// WINGMAN_PROVIDER_LIVE=1 go test -run TestResponsesLiveCrossProviderReplay -v.
func TestResponsesLiveCrossProviderReplay(t *testing.T) {
	if os.Getenv("WINGMAN_PROVIDER_LIVE") != "1" {
		t.Skip("set WINGMAN_PROVIDER_LIVE=1 to test real provider switches")
	}
	values, _ := godotenv.Read("../../../.env")
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_BEARER_TOKEN_BEDROCK"} {
		if os.Getenv(name) == "" && values[name] != "" {
			t.Setenv(name, values[name])
		}
	}
	key := func(name string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		if values[name] == "" {
			t.Fatalf("%s is required", name)
		}
		return values[name]
	}
	client := &http.Client{Timeout: 90 * time.Second}
	gemini, err := google.NewCompleter("gemini-4-argon", google.WithToken(key("GEMINI_API_KEY")), google.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	gpt, err := openai.NewResponder("https://api.openai.com/v1", "gpt-5.4-mini", openai.WithToken(key("OPENAI_API_KEY")), openai.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	claude, err := anthropic.NewCompleter("https://api.anthropic.com", "claude-sonnet-5-5", anthropic.WithToken(key("ANTHROPIC_API_KEY")), anthropic.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	converse, err := bedrock.NewCompleter("eu.anthropic.claude-sonnet-4-6", bedrock.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	for name, completer := range map[string]provider.Completer{"gemini": gemini, "gpt": gpt, "claude": claude, "bedrock": converse} {
		cfg.RegisterCompleter(name, signatures.ScopedTo(name, completer))
	}
	router := chi.NewRouter()
	router.Route("/v1", New(cfg).Attach)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	for _, stream := range []bool{false, true} {
		for _, cycle := range [][]string{
			{"gemini", "gpt", "claude", "bedrock"},
			{"bedrock", "gemini", "claude", "gpt"},
			{"gpt", "bedrock", "claude", "gemini"},
			{"gemini", "bedrock", "gpt", "gemini"},
			{"gemini", "gemini", "gemini", "gemini"},
			{"gpt", "gpt", "gpt", "gpt"},
			{"claude", "claude", "claude", "claude"},
			{"bedrock", "bedrock", "bedrock", "bedrock"},
		} {
			t.Run(fmt.Sprintf("%v/stream=%t", cycle, stream), func(t *testing.T) {
				input := []map[string]any{{"role": "user", "content": "Inspect a repository in exactly three sequential tool stages. Stage 1: call glob for **/AGENTS.md. Wait for its result. Stage 2: call run_command for git log --oneline -5. Wait for its result. Stage 3: call glob for pkg/agent/*.go. Wait for its result. Finally reply with DONE. Make exactly one tool call per stage and do not skip any stage."}}
				for turn, model := range cycle {
					output := postGeminiResponses(t, server.URL, model, input, geminiReplayTools(), stream)
					input = append(input, output...)
					var calls []map[string]any
					var kinds []string
					for _, item := range output {
						kind := item["type"].(string)
						kinds = append(kinds, kind)
						if kind == "function_call" {
							calls = append(calls, item)
						}
					}
					t.Logf("turn %d (%s): %v", turn+1, model, kinds)
					if turn == 3 {
						encoded, _ := json.Marshal(output)
						if len(calls) != 0 || !bytes.Contains(encoded, []byte("DONE")) {
							t.Fatalf("final turn did not answer DONE: %s", encoded)
						}
						continue
					}
					if len(calls) != 1 {
						t.Fatalf("stage %d made %d calls", turn+1, len(calls))
					}
					wantName := []string{"glob", "run_command", "glob"}[turn]
					if calls[0]["name"] != wantName {
						t.Fatalf("stage %d called %v, want %s", turn+1, calls[0]["name"], wantName)
					}
					input = append(input, map[string]any{"type": "function_call_output", "call_id": calls[0]["call_id"], "output": "Tool completed successfully. Continue to the next stage."})
				}
			})
		}
	}
}
