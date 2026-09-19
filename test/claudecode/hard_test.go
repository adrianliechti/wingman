package claudecode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	claude "github.com/adrianliechti/wingman/pkg/provider/anthropic"
	server "github.com/adrianliechti/wingman/server/anthropic"
	"github.com/adrianliechti/wingman/test/anthropic"
	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/go-chi/chi/v5"
)

func claudeBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("CLAUDE_CODE_BIN")
	if binary == "" {
		binary = "claude"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		t.Fatalf("Claude Code is required: %v", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// TestClaudeCodeHard covers what the base scenarios avoid: the CLI's default
// tool set, an image returned by a tool, and high-effort thinking replayed
// across a multi-turn repair. Each run logs the wire features it used.
func TestClaudeCodeHard(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_LIVE") != "1" {
		t.Skip("set CLAUDE_CODE_LIVE=1 to run paid Claude Code comparison tests")
	}
	h := anthropic.New(t)
	binary := claudeBinary(t)

	repair := projectScenario(t)
	repair.name = "thinking_repair"
	repair.options.effort = "high"
	repair.options.maxTurns = 16
	repair.options.timeout = 5 * time.Minute
	repairVerify := repair.verify
	repair.verify = func(t *testing.T, exchanges []exchange, dir string) {
		repairVerify(t, exchanges, dir)
		requireSignedThinkingReplay(t, exchanges)
	}

	for _, scenario := range []claudeScenario{
		{
			name:    "default_tools",
			prompt:  "Reply with exactly WINGMAN_E2E_OK and nothing else. Do not use tools.",
			want:    "WINGMAN_E2E_OK",
			options: claudeRunOptions{defaultTools: true},
		},
		{
			name:   "image_read",
			prompt: "Use Read on red.png in the current working directory, then reply with the dominant color of the image as one lowercase English word and nothing else.",
			tools:  "Read",
			want:   "red",
			options: claudeRunOptions{setup: func(t *testing.T, dir string) {
				writeArtifact(t, filepath.Join(dir, "red.png"), hardtest.RedSquarePNG())
			}},
		},
		repair,
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := func(t *testing.T, endpoint harness.Endpoint, model string) outcome {
				answer, exchanges, fixture := runClaudeWithOptions(t, binary, endpoint, model, scenario.prompt, scenario.tools, scenario.input, scenario.options)
				tools := checkExchanges(t, exchanges, model)
				logFeatures(t, exchanges)
				if scenario.verify != nil {
					scenario.verify(t, exchanges, fixture)
				}
				switch scenario.name {
				case "image_read":
					answer = strings.ToLower(strings.Trim(answer, " .\n\""))
				case "thinking_repair":
					// At high effort the model narrates before the marker.
					if strings.Contains(answer, scenario.want) {
						answer = scenario.want
					}
				}
				if answer != scenario.want {
					t.Errorf("outcome %q, want %q", answer, scenario.want)
				}
				return outcome{answer, tools}
			}
			var reference outcome
			referenceRan := false
			if !t.Run("anthropic", func(t *testing.T) {
				reference = run(t, h.Anthropic, h.ReferenceModel)
				referenceRan = true
			}) {
				t.Fatal("Anthropic reference failed; cannot compare Wingman")
			}
			for _, model := range anthropic.DefaultModels() {
				t.Run("wingman/"+model.Name, func(t *testing.T) {
					h.SkipUnlessConfigured(t, model.Name)
					actual := run(t, h.Wingman, model.Name)
					if referenceRan && !reflect.DeepEqual(actual, reference) {
						t.Errorf("outcome differs from Anthropic: got %+v, want %+v", actual, reference)
					}
				})
			}
		})
	}
}

// logFeatures records the request parameters, tool types, betas and answered
// block types of a run, so a diff between the reference and Wingman logs
// shows what the CLI relies on and what the gateway answered differently.
func logFeatures(t *testing.T, exchanges []exchange) {
	t.Helper()
	for _, line := range harness.Inventory(exchanges).Lines() {
		for _, prefix := range []string{"request key:", "request beta:", "request tool", "request param:", "request cache_control:", "response block:", "response stop:", "response status:"} {
			if strings.HasPrefix(line, prefix) {
				t.Log(line)
				break
			}
		}
	}
}

// requireSignedThinkingReplay checks that every thinking block Claude Code
// replays carries its signature, which the backend needs to accept the turn.
func requireSignedThinkingReplay(t *testing.T, exchanges []exchange) {
	t.Helper()
	replayed := 0
	for i, record := range exchanges {
		if !strings.HasPrefix(record.Path, "/v1/messages") || record.Status != 200 {
			continue
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type      string `json:"type"`
					Signature string `json:"signature"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
			continue
		}
		for _, message := range request.Messages {
			for _, block := range message.Content {
				if message.Role == "assistant" && block.Type == "thinking" {
					replayed++
					if block.Signature == "" {
						t.Errorf("exchange %d: replayed thinking block without a signature", i)
					}
				}
			}
		}
	}
	if replayed == 0 {
		t.Log("no thinking blocks were replayed; the model did not think at high effort")
	} else {
		t.Logf("%d signed thinking blocks replayed", replayed)
	}
}

// TestClaudeCodeUpstream places Wingman between Claude Code and Anthropic
// with both hops recorded. It checks that the upstream request prefix stays
// stable from turn to turn, so Anthropic's prompt cache keeps hitting, and
// reports which client features the gateway dropped, added or rewrote.
func TestClaudeCodeUpstream(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_LIVE") != "1" {
		t.Skip("set CLAUDE_CODE_LIVE=1 to run paid Claude Code comparison tests")
	}
	h := anthropic.New(t)
	binary := claudeBinary(t)
	model := h.ReferenceModel

	upstream := harness.NewRecorder(t, strings.TrimSuffix(h.Anthropic.BaseURL, "/v1"), http.Header{"X-Api-Key": {h.Anthropic.APIKey}}, 0)
	completer, err := claude.NewCompleter(upstream.URL, model, claude.WithToken("recorded"), claude.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, completer))
	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) { server.New(cfg).Attach(r) })
	local := httptest.NewServer(router)
	defer local.Close()
	endpoint := harness.Endpoint{Name: "wingman", BaseURL: local.URL + "/v1", APIKey: "test-key"}

	scenario := projectScenario(t)
	answer, exchanges, fixture := runClaudeWithOptions(t, binary, endpoint, model, scenario.prompt, scenario.tools, scenario.input, scenario.options)
	checkExchanges(t, exchanges, model)
	scenario.verify(t, exchanges, fixture)
	if answer != scenario.want {
		t.Errorf("outcome %q, want %q", answer, scenario.want)
	}

	requests := upstream.Exchanges()
	saveUpstream(t, "CLAUDE_CODE_ARTIFACTS", requests)
	for _, line := range harness.Compare(harness.Inventory(exchanges), harness.Inventory(requests)) {
		t.Logf("client vs upstream: %s", line)
	}
	requireStablePrefix(t, requests, "/v1/messages", []string{"model", "system", "tools", "thinking", "output_config", "max_tokens", "stream"}, "messages")
	requireCacheReads(t, requests)
}

// requireStablePrefix checks that the fixed request fields and the replayed
// history serialize identically from one upstream turn to the next. Any
// difference moves the cached prefix and turns a cache read into a rewrite.
func requireStablePrefix(t *testing.T, exchanges []harness.Exchange, pathPrefix string, fixed []string, history string) {
	t.Helper()
	var previous map[string]any
	turn := 0
	for _, record := range exchanges {
		if !strings.HasPrefix(record.Path, pathPrefix) || record.Status != 200 {
			continue
		}
		var request map[string]any
		if err := json.Unmarshal([]byte(record.Request), &request); err != nil {
			t.Fatalf("upstream request is not JSON: %v", err)
		}
		stripKey(request, "cache_control")
		turn++
		if previous != nil {
			for _, key := range fixed {
				if !reflect.DeepEqual(previous[key], request[key]) {
					t.Errorf("upstream turn %d: %q changed between requests, which invalidates the prompt cache\n  before: %.400s\n  after:  %.400s", turn, key, compactJSON(previous[key]), compactJSON(request[key]))
				}
			}
			before, _ := previous[history].([]any)
			after, _ := request[history].([]any)
			if len(after) < len(before) {
				t.Errorf("upstream turn %d: %s shrank from %d to %d items", turn, history, len(before), len(after))
			}
			for i := 0; i < len(before) && i < len(after); i++ {
				if !reflect.DeepEqual(before[i], after[i]) {
					t.Errorf("upstream turn %d: %s[%d] changed between requests, which invalidates the prompt cache\n  before: %.600s\n  after:  %.600s", turn, history, i, compactJSON(before[i]), compactJSON(after[i]))
					break
				}
			}
		}
		previous = request
	}
	if turn < 2 {
		t.Errorf("expected at least two upstream turns, got %d", turn)
	}
}

// requireCacheReads checks Anthropic's usage on every upstream turn after
// the first: a stable prefix shows up as cache_read_input_tokens.
func requireCacheReads(t *testing.T, exchanges []harness.Exchange) {
	t.Helper()
	turn := 0
	for _, record := range exchanges {
		if !strings.HasPrefix(record.Path, "/v1/messages") || record.Status != 200 {
			continue
		}
		turn++
		events, err := harness.ParseSSE(strings.NewReader(record.Response))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Event != "message_start" {
				continue
			}
			message, _ := event.Data["message"].(map[string]any)
			usage, _ := message["usage"].(map[string]any)
			read, _ := usage["cache_read_input_tokens"].(float64)
			created, _ := usage["cache_creation_input_tokens"].(float64)
			input, _ := usage["input_tokens"].(float64)
			t.Logf("upstream turn %d: input=%v cache_read=%v cache_creation=%v", turn, input, read, created)
			if turn > 1 && read == 0 {
				t.Errorf("upstream turn %d: no prompt cache read; the previous turn cached %v tokens that this request could not reuse", turn, created)
			}
		}
	}
}

// saveUpstream retains the recorded upstream exchanges next to the client
// artifacts when an artifacts directory is configured.
func saveUpstream(t *testing.T, env string, exchanges []harness.Exchange) {
	t.Helper()
	root := os.Getenv(env)
	if root == "" {
		return
	}
	data, err := json.MarshalIndent(exchanges, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, strings.ReplaceAll(t.Name(), "/", "-")+"-upstream.json")
	writeArtifact(t, path, data)
	t.Logf("upstream exchanges: %s", path)
}

func stripKey(v any, key string) {
	switch value := v.(type) {
	case map[string]any:
		delete(value, key)
		for _, nested := range value {
			stripKey(nested, key)
		}
	case []any:
		for _, nested := range value {
			stripKey(nested, key)
		}
	}
}

func compactJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
