package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/adapter/signatures"
	oai "github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server/openai/responses"
	"github.com/adrianliechti/wingman/test/hardtest"
	"github.com/adrianliechti/wingman/test/harness"
	"github.com/adrianliechti/wingman/test/openai"
	"github.com/go-chi/chi/v5"
)

// TestCodexHard covers what the base scenarios avoid: parallel tool calls in
// one turn, an image the CLI attaches for the model, the hosted web_search
// tool Codex advertises, and automatic compaction of a long conversation.
// Each run logs the wire features it used.
func TestCodexHard(t *testing.T) {
	if os.Getenv("CODEX_LIVE") != "1" {
		t.Skip("set CODEX_LIVE=1 to run paid Codex comparison tests")
	}
	h := openai.New(t)
	binary := codexBinary(t, true)

	// The repair conversation passes this limit after the first tool results,
	// so Codex compacts at least once and typically several times. The
	// scenario keeps its own fixture: verification compares against the
	// files that setup wrote.
	compactionProject := harness.NewProjectFixture(t)
	compaction := projectScenario(t)
	compaction.name = "auto_compact"
	compaction.options.setup = compactionProject.Setup
	compaction.options.autoCompactTokenLimit = 4000
	compaction.options.maxRequests = 30
	compaction.options.timeout = 6 * time.Minute
	// Compaction discards the tool outputs, so the model re-reads files and
	// may retry a patch afterwards; the exact command sequence is the base
	// scenario's concern. Here the repaired project and the protocol count.
	compaction.verify = func(t *testing.T, exchanges []harness.Exchange, _ []cliEvent, dir string) {
		compactionProject.Verify(t, dir)
		logCompactions(t, exchanges)
	}

	for _, scenario := range []codexScenario{
		{
			name:   "parallel_shell",
			prompt: "Use two separate exec_command calls in the same turn, in parallel, to run exactly `cat a.txt` and exactly `cat b.txt` in the current directory. After both results, reply with exactly the content of a.txt, a plus sign, and the content of b.txt, without spaces or newlines. Do not use other tools.",
			want:   "alpha+beta",
			options: codexRunOptions{setup: func(t *testing.T, dir string) {
				writeArtifact(t, filepath.Join(dir, "a.txt"), []byte("alpha\n"))
				writeArtifact(t, filepath.Join(dir, "b.txt"), []byte("beta\n"))
			}},
			verify: func(t *testing.T, exchanges []harness.Exchange, _ []cliEvent, _ string) {
				logParallelCalls(t, exchanges)
			},
		},
		{
			name:   "view_image",
			prompt: "Use the view_image tool on red.png in the current directory, then reply with the dominant color of the image as one lowercase English word and nothing else. Do not use other tools.",
			want:   "red",
			options: codexRunOptions{setup: func(t *testing.T, dir string) {
				writeArtifact(t, filepath.Join(dir, "red.png"), hardtest.RedSquarePNG())
			}},
		},
		{
			name:    "web_search",
			prompt:  "Reply with exactly WINGMAN_E2E_OK and nothing else. Do not use tools.",
			want:    "WINGMAN_E2E_OK",
			options: codexRunOptions{webSearch: "live"},
		},
		compaction,
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run := func(t *testing.T, endpoint harness.Endpoint, model string) outcome {
				actual, exchanges, fixture, events := runCodexWithOptions(t, binary, endpoint, model, scenario.prompt, scenario.input, scenario.options)
				checkExchanges(t, exchanges, model)
				logFeatures(t, exchanges)
				if scenario.verify != nil {
					scenario.verify(t, exchanges, events, fixture)
				}
				actual.Answer = strings.TrimSpace(actual.Answer)
				if scenario.name == "view_image" {
					actual.Answer = strings.ToLower(strings.Trim(actual.Answer, " .\n\""))
				}
				if actual.Answer != scenario.want {
					t.Errorf("outcome %q, want %q", actual.Answer, scenario.want)
				}
				return actual
			}
			var reference outcome
			referenceRan := false
			if !t.Run("openai", func(t *testing.T) {
				reference = run(t, h.OpenAI, h.ReferenceModel)
				referenceRan = true
			}) {
				t.Fatal("OpenAI reference failed; cannot compare Wingman")
			}
			for _, model := range openai.DefaultModels() {
				t.Run("wingman/"+model.Name, func(t *testing.T) {
					h.SkipUnlessConfigured(t, model.Name)
					actual := run(t, h.Wingman, model.Name)
					if referenceRan && actual != reference {
						t.Errorf("outcome differs from OpenAI: got %+v, want %+v", actual, reference)
					}
				})
			}
		})
	}
}

// logFeatures records the request parameters, tool types and answered item
// types of a run, so a diff between the reference and Wingman logs shows
// what the CLI relies on and what the gateway answered differently.
func logFeatures(t *testing.T, exchanges []harness.Exchange) {
	t.Helper()
	for _, line := range harness.Inventory(exchanges).Lines() {
		for _, prefix := range []string{"request key:", "request tool", "request param:", "request item:", "response item:", "response response_status:", "response status:"} {
			if strings.HasPrefix(line, prefix) {
				t.Log(line)
				break
			}
		}
	}
}

// logCompactions counts the checkpoint summaries Codex requested and the
// fresh threads it started from them; both are ordinary Responses requests.
func logCompactions(t *testing.T, exchanges []harness.Exchange) {
	t.Helper()
	summaries, resumes := 0, 0
	for _, record := range exchanges {
		if strings.Contains(record.Request, "CONTEXT CHECKPOINT COMPACTION") {
			summaries++
		}
		if strings.Contains(record.Request, "Another language model started to solve this problem") {
			resumes++
		}
	}
	if summaries == 0 {
		t.Error("Codex never compacted; lower autoCompactTokenLimit for this project size")
	}
	t.Logf("compaction: %d checkpoint summaries, %d resumed threads", summaries, resumes)
}

// logParallelCalls reports the largest number of tool calls one response
// carried; the model decides whether to parallelize, so this is informational
// while the outcome check proves both calls were served.
func logParallelCalls(t *testing.T, exchanges []harness.Exchange) {
	t.Helper()
	most := 0
	for _, record := range exchanges {
		events, err := harness.ParseSSE(strings.NewReader(record.Response))
		if err != nil {
			continue
		}
		calls := 0
		for _, event := range events {
			if event.Data["type"] != "response.output_item.added" {
				continue
			}
			item, _ := event.Data["item"].(map[string]any)
			if item["type"] == "function_call" || item["type"] == "custom_tool_call" {
				calls++
			}
		}
		most = max(most, calls)
	}
	t.Logf("most tool calls in one response: %d", most)
}

// TestCodexUpstream places Wingman between Codex and OpenAI with both hops
// recorded. It checks that the upstream request prefix stays stable from
// turn to turn, so OpenAI's prompt cache keeps hitting, and reports which
// client features the gateway dropped, added or rewrote.
func TestCodexUpstream(t *testing.T) {
	if os.Getenv("CODEX_LIVE") != "1" {
		t.Skip("set CODEX_LIVE=1 to run paid Codex comparison tests")
	}
	h := openai.New(t)
	binary := codexBinary(t, true)
	model := h.ReferenceModel

	upstream := harness.NewRecorder(t, strings.TrimSuffix(h.OpenAI.BaseURL, "/v1"), http.Header{"Authorization": {"Bearer " + h.OpenAI.APIKey}}, 0)
	completer, err := oai.NewResponder(upstream.URL+"/v1", model, oai.WithToken("recorded"), oai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter(model, signatures.ScopedTo(model, completer))
	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) { responses.New(cfg).Attach(r) })
	local := httptest.NewServer(router)
	defer local.Close()
	endpoint := harness.Endpoint{Name: "wingman", BaseURL: local.URL + "/v1", APIKey: "test-key"}

	scenario := projectScenario(t)
	actual, exchanges, fixture, events := runCodexWithOptions(t, binary, endpoint, model, scenario.prompt, scenario.input, scenario.options)
	checkExchanges(t, exchanges, model)
	scenario.verify(t, exchanges, events, fixture)
	if strings.TrimSpace(actual.Answer) != scenario.want {
		t.Errorf("outcome %q, want %q", actual.Answer, scenario.want)
	}

	requests := upstream.Exchanges()
	saveUpstream(t, "CODEX_ARTIFACTS", requests)
	for _, line := range harness.Compare(harness.Inventory(exchanges), harness.Inventory(requests)) {
		t.Logf("client vs upstream: %s", line)
	}
	requireStablePrefix(t, requests, "/v1/responses", []string{"model", "instructions", "tools", "reasoning", "text", "include", "parallel_tool_calls", "tool_choice", "store", "truncation", "prompt_cache_key"}, "input")
	requireCachedTokens(t, requests)
}

// requireStablePrefix checks that the fixed request fields and the replayed
// history serialize identically from one upstream turn to the next. Any
// difference moves the cached prefix and turns a cache hit into a miss.
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

// requireCachedTokens checks OpenAI's usage on every upstream turn after the
// first: a stable prefix shows up as input_tokens_details.cached_tokens.
func requireCachedTokens(t *testing.T, exchanges []harness.Exchange) {
	t.Helper()
	turn := 0
	for _, record := range exchanges {
		if !strings.HasPrefix(record.Path, "/v1/responses") || record.Status != 200 {
			continue
		}
		turn++
		events, err := harness.ParseSSE(strings.NewReader(record.Response))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Data["type"] != "response.completed" {
				continue
			}
			response, _ := event.Data["response"].(map[string]any)
			usage, _ := response["usage"].(map[string]any)
			details, _ := usage["input_tokens_details"].(map[string]any)
			cached, _ := details["cached_tokens"].(float64)
			input, _ := usage["input_tokens"].(float64)
			t.Logf("upstream turn %d: input=%v cached=%v", turn, input, cached)
			if turn > 1 && cached == 0 {
				t.Errorf("upstream turn %d: no cached input tokens; the request prefix did not match the previous turn", turn)
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

func compactJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
