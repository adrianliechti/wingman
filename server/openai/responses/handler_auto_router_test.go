package responses

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
)

// Exercise the same configured router, native decision adapter, provider
// adapters and Responses endpoint with either scripted or live upstreams.
func autoRouterServer(t *testing.T, openaiURL, anthropicURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "router.yaml")
	data := fmt.Sprintf(`
providers:
  - type: openai
    url: %s
    token: ${ROUTER_TEST_OPENAI_KEY}
    models: [gpt-6-luna, gpt-6.2-sol]
  - type: anthropic
    url: %s
    token: ${ROUTER_TEST_ANTHROPIC_KEY}
    models: [claude-sonnet-5-5]
routers:
  auto:
    type: auto
    decider: gpt-6-luna
    routes:
      - model: gpt-6.2-sol
        when: The latest request asks for arithmetic or a number.
      - model: claude-sonnet-5-5
        when: The latest request asks for poetry or creative writing.
`, openaiURL, anthropicURL)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.Close() })
	mux := chi.NewRouter()
	mux.Route("/v1", New(cfg).Attach)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

func TestAutoRouterCrossProviderReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			const gpt, claude = "gpt-6.2-sol", "claude-sonnet-5-5"
			t.Setenv("ROUTER_TEST_OPENAI_KEY", "test")
			t.Setenv("ROUTER_TEST_ANTHROPIC_KEY", "test")
			var members []string
			var decisions int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				switch r.URL.Path {
				case "/v1/decisions":
					decisions++
					var req struct {
						Input     string
						Questions []struct {
							Type    string
							Choices []struct{ Value, Description string }
						}
					}
					if err := json.Unmarshal(body, &req, json.MatchCaseInsensitiveNames(true)); err != nil || len(req.Questions) != 1 || req.Questions[0].Type != "choice" || len(req.Questions[0].Choices) != 2 {
						t.Errorf("invalid decision request: %s", body)
						return
					}
					var state struct {
						Request string `json:"request"`
					}
					if err := json.Unmarshal([]byte(req.Input), &state); err != nil {
						t.Error(err)
					}
					if strings.Contains(req.Input, "SIG_1") || strings.Contains(req.Input, "GPT_OPAQUE") || strings.Contains(req.Input, "secret tool output") {
						t.Error("private replay content reached the picker")
					}
					selected, other := gpt, claude
					if strings.Contains(state.Request, "poem") {
						selected, other = claude, gpt
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"model":"gpt-6-luna","answers":[{"name":"model","type":"choice","choice":%q,"confidence":0.99,"probabilities":[{"value":%q,"probability":0.99},{"value":%q,"probability":0.01}]}]}`, selected, selected, other)
				case "/v1/responses":
					replay := string(body)
					if strings.Contains(replay, "SIG_1") {
						t.Error("Claude signature reached GPT")
					}
					if len(members) > 0 {
						for _, want := range []string{"secret tool output", "call_2", "GPT_OPAQUE"} {
							if !strings.Contains(replay, want) {
								t.Errorf("GPT replay is missing %q", want)
							}
						}
					}
					members = append(members, gpt)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"model":"gpt-6.2-sol","status":"completed","output":[{"type":"reasoning","id":"rs_gpt","summary":[],"encrypted_content":"GPT_OPAQUE"},{"type":"message","id":"msg_gpt","role":"assistant","content":[{"type":"output_text","text":"The number is 17."}]}]}}`+"\n\n")
				case "/v1/messages":
					if strings.Contains(string(body), "GPT_OPAQUE") || !strings.Contains(string(body), "The number is 17.") || (len(members) == 3 && !strings.Contains(string(body), "SIG_1")) {
						t.Errorf("Claude received invalid replay: %s", body)
					}
					members = append(members, claude)
					w.Header().Set("Content-Type", "text/event-stream")
					if strings.Contains(string(body), "Another poem") {
						// A real Anthropic stream can disconnect after message_start.
						// The GPT fallback must receive the original replay.
						fmt.Fprint(w, "event: message_start\ndata: "+`{"type":"message_start","message":{"id":"partial","type":"message","role":"assistant","content":[]}}`+"\n\n")
						return
					}
					w.Write(anthropicContinuationEvents(len(members) == 2))
				default:
					t.Errorf("unexpected provider path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			url := autoRouterServer(t, upstream.URL+"/v1", upstream.URL)
			var input []map[string]any
			for i, prompt := range []string{"Give me a number.", "Write a poem about it using glob.", "Repeat the number.", "Finish the poem.", "Another poem, please."} {
				input = append(input, map[string]any{"role": "user", "content": prompt})
				response := postReplayResponse(t, url, "auto", input, geminiReplayTools(), stream)
				if i == 4 && response.Model != gpt {
					t.Fatalf("expected GPT after Claude disconnected, got %s", response.Model)
				}
				output := response.Output
				input = append(input, output...)
				if i == 1 {
					if len(output) != 2 || output[0]["type"] != "reasoning" || output[1]["call_id"] != "call_2" {
						t.Fatalf("expected signed reasoning and tool call, got %v", output)
					}
					input = append(input, map[string]any{"type": "function_call_output", "call_id": output[1]["call_id"], "output": "secret tool output"})
				}
			}
			if decisions != 5 || !slices.Equal(members, []string{gpt, claude, gpt, claude, claude, gpt}) {
				t.Fatalf("%d decisions routed to %v", decisions, members)
			}
		})
	}
}

func TestAutoRouterLiveCrossProviderReplay(t *testing.T) {
	if os.Getenv("WINGMAN_ROUTER_LIVE") != "1" {
		t.Skip("set WINGMAN_ROUTER_LIVE=1 to run paid decision and completion calls")
	}
	values, _ := godotenv.Read("../../../.env")
	for key, target := range map[string]string{"OPENAI_API_KEY": "ROUTER_TEST_OPENAI_KEY", "ANTHROPIC_API_KEY": "ROUTER_TEST_ANTHROPIC_KEY"} {
		value := os.Getenv(key)
		if value == "" {
			value = values[key]
		}
		if value == "" {
			t.Fatalf("%s is required", key)
		}
		t.Setenv(target, value)
	}
	url := autoRouterServer(t, "https://api.openai.com/v1", "https://api.anthropic.com")
	var input []map[string]any
	for i, turn := range []struct{ prompt, model, word string }{
		{"What is seven times six? Answer with digits only.", "gpt-6.2-sol", "42"},
		{"Write a one-line poem about that answer. Include its digits and the word lantern.", "claude-sonnet-5-5", "lantern"},
		{"Arithmetic: double the original answer. Reply in digits only.", "gpt-6.2-sol", "84"},
	} {
		input = append(input, map[string]any{"role": "user", "content": turn.prompt})
		response := postReplayResponse(t, url, "auto", input, nil, i == 1)
		t.Logf("turn %d used %s", i+1, response.Model)
		if !strings.HasPrefix(response.Model, turn.model) {
			t.Fatalf("wanted %s, got %s", turn.model, response.Model)
		}
		var text strings.Builder
		for _, item := range response.Output {
			if item["type"] != "message" || item["phase"] == "commentary" {
				continue
			}
			parts, _ := item["content"].([]any)
			for _, part := range parts {
				block, _ := part.(map[string]any)
				if block["type"] == "output_text" {
					value, _ := block["text"].(string)
					text.WriteString(value)
				}
			}
		}
		answer := strings.ToLower(text.String())
		if !strings.Contains(answer, turn.word) || (i == 1 && !strings.Contains(answer, "42")) {
			t.Fatalf("unexpected answer: %s", answer)
		}
		input = append(input, response.Output...)
	}
}
