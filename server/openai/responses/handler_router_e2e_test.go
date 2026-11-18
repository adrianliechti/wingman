package responses

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
	"github.com/go-chi/chi/v5"
)

// The tests below run the configured routers, the native decision adapter, the
// provider adapters and the Responses endpoint against a scripted upstream.

const (
	e2eGPT    = "gpt-6.2-sol"
	e2eClaude = "claude-sonnet-5-5"

	e2eAutoRouter = `
routers:
  auto:
    type: auto
    decider: gpt-6-luna
    routes:
      - model: gpt-6.2-sol
        when: The latest request asks for arithmetic or a number.
      - model: claude-sonnet-5-5
        when: The latest request asks for poetry or creative writing.
`
)

// upstreamCall is one request Wingman sent to a provider.
type upstreamCall struct {
	Path  string
	Model string
	Body  string
}

// upstreamLog counts provider calls by a name the script chooses.
type upstreamLog struct {
	mu     sync.Mutex
	counts map[string]int
}

func (l *upstreamLog) add(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil {
		l.counts = make(map[string]int)
	}
	l.counts[name]++
	return l.counts[name]
}

func (l *upstreamLog) count(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[name]
}

// routerE2E starts Wingman with the given routers in front of a scripted
// upstream that plays OpenAI and Anthropic. providers adds provider entries.
func routerE2E(t *testing.T, providers, routers string, script func(http.ResponseWriter, *http.Request, upstreamCall)) (*config.Config, string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &request)
		script(w, r, upstreamCall{Path: r.URL.Path, Model: request.Model, Body: string(body)})
	}))
	t.Cleanup(upstream.Close)

	path := filepath.Join(t.TempDir(), "router.yaml")
	data := fmt.Sprintf(`
providers:
  - type: openai
    url: %[1]s/v1
    token: test
    models: [gpt-6-luna, gpt-6.2-sol, gpt-6.2-astra]
  - type: anthropic
    url: %[1]s
    token: test
    models: [claude-sonnet-5-5, claude-opus-5-5]
`, upstream.URL) + strings.ReplaceAll(providers, "UPSTREAM", upstream.URL) + routers
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
	return cfg, server.URL
}

// decisionRequest is what the decider was asked: the request excerpt, the
// raw evidence and the routes on offer.
type decisionRequest struct {
	Request string
	Input   string
	Choices []string
}

func parseDecision(t *testing.T, body string) decisionRequest {
	t.Helper()
	var req struct {
		Input     string
		Questions []struct {
			Type    string
			Choices []struct{ Value string }
		}
	}
	if err := json.Unmarshal([]byte(body), &req, json.MatchCaseInsensitiveNames(true)); err != nil || len(req.Questions) != 1 || req.Questions[0].Type != "choice" {
		t.Errorf("invalid decision request: %s", body)
		return decisionRequest{}
	}
	var state struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal([]byte(req.Input), &state); err != nil {
		t.Error(err)
	}
	decision := decisionRequest{Request: state.Request, Input: req.Input}
	for _, choice := range req.Questions[0].Choices {
		decision.Choices = append(decision.Choices, choice.Value)
	}
	return decision
}

func writeDecision(w http.ResponseWriter, selected string, choices []string) {
	var probabilities []string
	for _, choice := range choices {
		probability := 0.01 / float64(max(len(choices)-1, 1))
		if choice == selected {
			probability = 0.99
		}
		probabilities = append(probabilities, fmt.Sprintf(`{"value":%q,"probability":%g}`, choice, probability))
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"model":"gpt-6-luna","answers":[{"name":"model","type":"choice","choice":%q,"confidence":0.99,"probabilities":[%s]}]}`, selected, strings.Join(probabilities, ","))
}

// poemsToClaude answers a decision the way the route descriptions read.
func poemsToClaude(t *testing.T, w http.ResponseWriter, call upstreamCall) decisionRequest {
	t.Helper()
	decision := parseDecision(t, call.Body)
	selected := e2eGPT
	if strings.Contains(strings.ToLower(decision.Request), "poem") {
		selected = e2eClaude
	}
	writeDecision(w, selected, decision.Choices)
	return decision
}

func writeOpenAIText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: "+`{"type":"response.completed","response":{"model":"gpt-6.2-sol","status":"completed","output":[{"type":"message","id":"msg_gpt","role":"assistant","content":[{"type":"output_text","text":%q}]}]}}`+"\n\n", text)
}

func writeOpenAIError(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":%q,"type":%q}}`, message, kind)
}

func anthropicEvent(w http.ResponseWriter, kind, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, payload)
	http.NewResponseController(w).Flush()
}

func anthropicStart(w http.ResponseWriter) {
	anthropicEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_e2e","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}`)
}

func anthropicEnd(w http.ResponseWriter) {
	anthropicEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
	anthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
}

func anthropicTextBlock(w http.ResponseWriter, index int, text string) {
	anthropicEvent(w, "content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":%q}}`, index, text))
	anthropicEvent(w, "content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}

func writeAnthropicText(w http.ResponseWriter, text string) {
	anthropicStart(w)
	anthropicTextBlock(w, 0, text)
	anthropicEnd(w)
}

// writeAnthropicThinking plays a model that reasons without streaming its
// thoughts: nothing follows the start of the thinking block until think returns.
func writeAnthropicThinking(w http.ResponseWriter, think func(), text string) {
	anthropicStart(w)
	anthropicEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
	think()
	anthropicEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG_SLOW"}}`)
	anthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
	anthropicTextBlock(w, 1, text)
	anthropicEnd(w)
}

func writeAnthropicError(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":%q}}`, kind, message)
}

func userInput(prompt string) []map[string]any {
	return []map[string]any{{"role": "user", "content": prompt}}
}

func postRouter(t *testing.T, url string, body map[string]any) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Post(url+"/v1/responses", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	out, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(out)
}

func outputText(response replayResponse) string {
	var text strings.Builder
	for _, item := range response.Output {
		if item["type"] != "message" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, part := range parts {
			block, _ := part.(map[string]any)
			value, _ := block["text"].(string)
			text.WriteString(value)
		}
	}
	return text.String()
}

func eachStreamMode(t *testing.T, test func(t *testing.T, stream bool)) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) { test(t, stream) })
	}
}

// A model that reasons in silence for longer than first_token_timeout has
// started to answer: it keeps the request. A model that never starts loses it.
func TestRouterFirstTokenDeadlineAllowsSilentThinking(t *testing.T) {
	routers := strings.Replace(e2eAutoRouter, "type: auto", "type: auto\n    first_token_timeout: 400ms", 1)
	eachStreamMode(t, func(t *testing.T, stream bool) {
		var calls upstreamLog
		_, url := routerE2E(t, "", routers, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIText(w, "From GPT")
			case "/v1/messages":
				calls.add(e2eClaude)
				if strings.Contains(call.Body, "stalled") {
					anthropicStart(w)
					<-r.Context().Done()
					return
				}
				writeAnthropicThinking(w, func() { time.Sleep(1200 * time.Millisecond) }, "Slow verse")
			}
		})

		response := postReplayResponse(t, url, "auto", userInput("Write a poem."), nil, stream)
		if response.Model != e2eClaude || outputText(response) != "Slow verse" || calls.count(e2eClaude) != 1 || calls.count(e2eGPT) != 0 {
			t.Fatalf("thinking model lost the request: %s answered %q (claude=%d gpt=%d)", response.Model, outputText(response), calls.count(e2eClaude), calls.count(e2eGPT))
		}

		response = postReplayResponse(t, url, "auto", userInput("Write a stalled poem."), nil, stream)
		if response.Model != e2eGPT || calls.count(e2eClaude) != 2 || calls.count(e2eGPT) != 1 {
			t.Fatalf("a model that never started kept the request: %s (claude=%d gpt=%d)", response.Model, calls.count(e2eClaude), calls.count(e2eGPT))
		}
	})
}

// A turn that ends without content is the model's answer. It reaches the
// client and neither moves to another model nor counts against its health.
func TestRouterReturnsEmptyAnswer(t *testing.T) {
	eachStreamMode(t, func(t *testing.T, stream bool) {
		var calls upstreamLog
		_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIText(w, "From GPT")
			case "/v1/messages":
				calls.add(e2eClaude)
				anthropicStart(w)
				anthropicEnd(w)
			}
		})

		// More turns than the failure threshold that would open the circuit.
		for turn := range 7 {
			status, body := postRouter(t, url, map[string]any{"model": "auto", "input": userInput("Write a poem."), "stream": stream})
			completed := strings.Contains(body, `"status":"completed"`) && !strings.Contains(body, "response.failed") && !strings.Contains(body, `"error":{`)
			if status != 200 || !completed || !strings.Contains(body, e2eClaude) || strings.Contains(body, "From GPT") {
				t.Fatalf("turn %d: HTTP %d: %s", turn, status, body)
			}
		}
		if calls.count(e2eClaude) != 7 || calls.count(e2eGPT) != 0 {
			t.Fatalf("empty answers were retried or opened the circuit: claude=%d gpt=%d", calls.count(e2eClaude), calls.count(e2eGPT))
		}
	})
}

// Routes are different models. What one rejects, another may accept.
func TestAutoRouterTriesOtherRoutesAfterRejection(t *testing.T) {
	tooLong := func(w http.ResponseWriter) {
		writeAnthropicError(w, 400, "invalid_request_error", "prompt is too long: 250000 tokens > 200000 maximum")
	}
	glob := []map[string]any{{"type": "function", "name": "glob", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}

	t.Run("another route accepts", func(t *testing.T) {
		var calls upstreamLog
		_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIText(w, "From GPT")
			case "/v1/messages":
				calls.add(e2eClaude)
				tooLong(w)
			}
		})
		// A rejection is not a failure: the route is asked again every turn.
		for range 7 {
			response := postReplayResponse(t, url, "auto", userInput("Write a poem."), nil, false)
			if response.Model != e2eGPT || outputText(response) != "From GPT" {
				t.Fatalf("rejected request was not served elsewhere: %s %q", response.Model, outputText(response))
			}
		}
		if calls.count(e2eClaude) != 7 || calls.count(e2eGPT) != 7 {
			t.Fatalf("rejection counted against the route: claude=%d gpt=%d", calls.count(e2eClaude), calls.count(e2eGPT))
		}
	})

	t.Run("every route rejects", func(t *testing.T) {
		var calls upstreamLog
		_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIError(w, 400, "invalid_request_error", "Your input exceeds the context window of this model.")
			case "/v1/messages":
				calls.add(e2eClaude)
				tooLong(w)
			}
		})
		status, body := postRouter(t, url, map[string]any{"model": "auto", "input": userInput("Write a poem.")})
		if status != 400 || !strings.Contains(body, "prompt is too long") || calls.count(e2eClaude) != 1 || calls.count(e2eGPT) != 1 {
			t.Fatalf("want the chosen route's rejection, got HTTP %d: %s (claude=%d gpt=%d)", status, body, calls.count(e2eClaude), calls.count(e2eGPT))
		}
	})

	t.Run("forced tool choice", func(t *testing.T) {
		var calls upstreamLog
		_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				calls.add(e2eGPT)
				if !strings.Contains(call.Body, `"tool_choice":"required"`) {
					t.Errorf("forced tool choice was weakened: %s", call.Body)
				}
				writeOpenAIText(w, "From GPT")
			case "/v1/messages":
				calls.add(e2eClaude)
				writeAnthropicText(w, "From Claude")
			}
		})
		// Claude Sonnet 5.5 cannot be forced to call a tool, GPT can.
		status, body := postRouter(t, url, map[string]any{"model": "auto", "input": userInput("Write a poem."), "tools": glob, "tool_choice": "required"})
		if status != 200 || !strings.Contains(body, "From GPT") || calls.count(e2eClaude) != 0 || calls.count(e2eGPT) != 1 {
			t.Fatalf("HTTP %d: %s (claude=%d gpt=%d)", status, body, calls.count(e2eClaude), calls.count(e2eGPT))
		}
	})

	t.Run("replicas keep the rejection", func(t *testing.T) {
		var calls upstreamLog
		pool := "\nrouters:\n  pool:\n    type: roundrobin\n    models: [claude-sonnet-5-5, gpt-6.2-sol]\n"
		_, url := routerE2E(t, "", pool, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIText(w, "From GPT")
			case "/v1/messages":
				calls.add(e2eClaude)
				tooLong(w)
			}
		})
		status, body := postRouter(t, url, map[string]any{"model": "pool", "input": userInput("Write a poem.")})
		if status != 400 || !strings.Contains(body, "prompt is too long") || calls.count(e2eGPT) != 0 {
			t.Fatalf("HTTP %d: %s (gpt=%d)", status, body, calls.count(e2eGPT))
		}
	})
}

// The stream opens as soon as the routed model is working, although the
// router still withholds its chunks.
func TestRouterOpensStreamWhileModelThinks(t *testing.T) {
	release := make(chan struct{})
	free := sync.OnceFunc(func() { close(release) })
	_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		switch call.Path {
		case "/v1/decisions":
			poemsToClaude(t, w, call)
		case "/v1/messages":
			writeAnthropicThinking(w, func() { <-release }, "Slow verse")
		}
	})
	// Registered last to run first: the servers wait for the model to finish.
	t.Cleanup(free)

	body, _ := json.Marshal(map[string]any{"model": "auto", "input": userInput("Write a poem."), "stream": true})
	opened := make(chan *http.Response, 1)
	go func() {
		response, err := http.Post(url+"/v1/responses", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Error(err)
			close(opened)
			return
		}
		opened <- response
	}()

	var response *http.Response
	select {
	case response = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("no response headers while the model was thinking")
	}
	if response == nil {
		t.FailNow()
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("HTTP %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}

	free()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "event: response.completed\n") || !strings.Contains(string(data), "Slow verse") {
		t.Fatalf("stream did not complete: %s", data)
	}
}

// Keepalives hold the connection open while a routed model reasons in silence,
// both within the router's two-minute first-token timeout and past it.
func TestRouterKeepaliveCoversSilentThinking(t *testing.T) {
	for _, thinks := range []time.Duration{90 * time.Second, 150 * time.Second} {
		t.Run(thinks.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				thinking := keepaliveCompleter(func(ctx context.Context, yield func(*provider.Completion, error) bool) {
					calls++
					message := func(content provider.Content) *provider.Completion {
						return &provider.Completion{ID: "msg", Model: "thinker", Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{content}}}
					}
					if !yield(&provider.Completion{ID: "msg", Model: "thinker"}, nil) || !yield(message(provider.ReasoningContent(provider.Reasoning{ID: "rs"})), nil) {
						return
					}
					select {
					case <-time.After(thinks):
					case <-ctx.Done():
						yield(nil, ctx.Err())
						return
					}
					if yield(message(provider.Content{Text: "Slow verse"}), nil) {
						yield(&provider.Completion{ID: "msg", Model: "thinker", Status: provider.CompletionStatusCompleted}, nil)
					}
				})
				pool, err := roundrobin.NewCompleter([]provider.Completer{thinking})
				if err != nil {
					t.Fatal(err)
				}

				w := &idleStreamWriter{ResponseRecorder: httptest.NewRecorder()}
				serveKeepaliveTest(w, pool)
				body := w.Body.String()
				if w.disconnected || calls != 1 || !strings.Contains(body, "event: response.completed\n") || !strings.Contains(body, "Slow verse") {
					t.Fatalf("stream lost during silent thinking (disconnected=%t calls=%d): %s", w.disconnected, calls, body)
				}
				if got, want := strings.Count(body, ": keep-alive\n\n"), int(thinks/(15*time.Second))-1; got < want {
					t.Fatalf("got %d keepalives during %s of thinking, want %d", got, thinks, want)
				}
			})
		})
	}
}

// With every member down, the fallback is the last resort. No first-token
// deadline cuts it off, since nothing is left to fail over to.
func TestRouterFallbackHasNoFirstTokenDeadline(t *testing.T) {
	pool := "\nrouters:\n  pool:\n    type: roundrobin\n    models: [gpt-6.2-sol]\n    fallback: claude-sonnet-5-5\n    first_token_timeout: 300ms\n"
	eachStreamMode(t, func(t *testing.T, stream bool) {
		var calls upstreamLog
		_, url := routerE2E(t, "", pool, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/responses":
				calls.add(e2eGPT)
				writeOpenAIError(w, 503, "server_error", "overloaded")
			case "/v1/messages":
				calls.add(e2eClaude)
				time.Sleep(900 * time.Millisecond)
				writeAnthropicText(w, "From the fallback")
			}
		})
		response := postReplayResponse(t, url, "pool", userInput("Give me a number."), nil, stream)
		if response.Model != e2eClaude || outputText(response) != "From the fallback" || calls.count(e2eGPT) != 1 || calls.count(e2eClaude) != 1 {
			t.Fatalf("slow fallback was cut off: %s %q", response.Model, outputText(response))
		}
	})
}

// A route that is itself a load balancer applies its own first-token timeout,
// so it can still fail over to its healthy member. The pool is declared after
// the router that names it.
func TestAutoRouterLeavesFirstTokenDeadlineToNestedPool(t *testing.T) {
	providers := `
  - type: openai
    url: UPSTREAM/stalled/v1
    token: test
    models:
      sol-stalled:
        id: gpt-6.2-sol
        type: completer
  - type: openai
    url: UPSTREAM/healthy/v1
    token: test
    models:
      sol-healthy:
        id: gpt-6.2-sol
        type: completer
`
	routers := `
routers:
  auto:
    type: auto
    decider: gpt-6-luna
    first_token_timeout: 300ms
    routes:
      - model: pool
        when: The latest request asks for arithmetic or a number.
      - model: claude-sonnet-5-5
        when: The latest request asks for poetry or creative writing.
  pool:
    type: roundrobin
    first_token_timeout: 300ms
    models: [sol-stalled, sol-healthy]
`
	eachStreamMode(t, func(t *testing.T, stream bool) {
		var calls upstreamLog
		_, url := routerE2E(t, providers, routers, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				writeDecision(w, "pool", parseDecision(t, call.Body).Choices)
			case "/stalled/v1/responses":
				calls.add("stalled")
				<-r.Context().Done()
			case "/healthy/v1/responses":
				calls.add("healthy")
				writeOpenAIText(w, "From the healthy member")
			case "/v1/messages":
				calls.add(e2eClaude)
				writeAnthropicText(w, "From Claude")
			}
		})
		response := postReplayResponse(t, url, "auto", userInput("Give me a number."), nil, stream)
		if outputText(response) != "From the healthy member" || calls.count("stalled") != 1 || calls.count("healthy") != 1 || calls.count(e2eClaude) != 0 {
			t.Fatalf("pool did not fail over on its own: %q (stalled=%d healthy=%d claude=%d)", outputText(response), calls.count("stalled"), calls.count("healthy"), calls.count(e2eClaude))
		}
	})
}

// A stream that was delivered in full stays a success when the caller's
// context ends with its last chunk.
func TestRouterDeliveredStreamEndsWithoutError(t *testing.T) {
	pool := "\nrouters:\n  pool:\n    type: roundrobin\n    models: [claude-sonnet-5-5]\n"
	cfg, _ := routerE2E(t, "", pool, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		writeAnthropicText(w, "Complete answer")
	})
	completer, err := cfg.Completer("pool")
	if err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{provider.UserMessage("Write a poem.")}

	chunks := 0
	for _, err := range completer.Complete(t.Context(), messages, nil) {
		if err != nil {
			t.Fatal(err)
		}
		chunks++
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var text strings.Builder
	seen := 0
	for chunk, err := range completer.Complete(ctx, messages, nil) {
		if err != nil {
			t.Fatalf("complete stream reported %v after chunk %d of %d", err, seen, chunks)
		}
		if chunk.Message != nil {
			text.WriteString(chunk.Message.Text())
		}
		if seen++; seen == chunks {
			cancel()
		}
	}
	if seen != chunks || text.String() != "Complete answer" {
		t.Fatalf("got %d of %d chunks: %q", seen, chunks, text.String())
	}
}

// The requests of one user turn share a decision: the turn stays on its model
// while tools run, and the decider is asked once.
func TestAutoRouterDecidesOncePerTurn(t *testing.T) {
	eachStreamMode(t, func(t *testing.T, stream bool) {
		var mu sync.Mutex
		var members []string
		var decisions []decisionRequest
		_, url := routerE2E(t, "", e2eAutoRouter, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			mu.Lock()
			defer mu.Unlock()
			switch call.Path {
			case "/v1/decisions":
				decisions = append(decisions, poemsToClaude(t, w, call))
			case "/v1/responses":
				members = append(members, e2eGPT)
				writeOpenAIText(w, "The number is 17.")
			case "/v1/messages":
				members = append(members, e2eClaude)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write(anthropicContinuationEvents(!strings.Contains(call.Body, "tool_result")))
			}
		})

		input := userInput("Write a poem about it using glob.")
		output := postReplayResponse(t, url, "auto", input, geminiReplayTools(), stream).Output
		if len(output) != 2 || output[1]["call_id"] != "call_2" {
			t.Fatalf("expected a tool call, got %v", output)
		}
		input = append(input, output...)
		input = append(input, map[string]any{"type": "function_call_output", "call_id": "call_2", "output": "three files"})
		for range 2 {
			if response := postReplayResponse(t, url, "auto", input, geminiReplayTools(), stream); outputText(response) != "DONE" {
				t.Fatalf("tool continuation: %v", response.Output)
			}
		}
		if len(decisions) != 1 || !slices.Equal(members, []string{e2eClaude, e2eClaude, e2eClaude}) {
			t.Fatalf("%d decisions for one turn, served by %v", len(decisions), members)
		}

		input = append(input, map[string]any{"role": "user", "content": "Give me a number."})
		if response := postReplayResponse(t, url, "auto", input, geminiReplayTools(), stream); response.Model != e2eGPT {
			t.Fatalf("new turn stayed on %s", response.Model)
		}
		if len(decisions) != 2 {
			t.Fatalf("%d decisions for two turns", len(decisions))
		}
	})
}

// After failure_threshold failed decisions the decider is left alone for
// recovery_timeout. Requests go to the first route without waiting for it,
// and the failure is logged.
func TestAutoRouterStopsAskingFailingDecider(t *testing.T) {
	var logs lockedBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	routers := strings.Replace(e2eAutoRouter, "type: auto", "type: auto\n    failure_threshold: 2\n    recovery_timeout: 400ms", 1)
	var failing atomic.Bool
	failing.Store(true)
	var calls upstreamLog
	_, url := routerE2E(t, "", routers, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		switch call.Path {
		case "/v1/decisions":
			calls.add("decider")
			if failing.Load() {
				writeOpenAIError(w, 500, "server_error", "decider down")
				return
			}
			poemsToClaude(t, w, call)
		case "/v1/responses":
			writeOpenAIText(w, "From GPT")
		case "/v1/messages":
			writeAnthropicText(w, "From Claude")
		}
	})

	for turn := range 5 {
		response := postReplayResponse(t, url, "auto", userInput(fmt.Sprintf("Write poem %d.", turn)), nil, false)
		if response.Model != e2eGPT {
			t.Fatalf("turn %d: undecided request went to %s", turn, response.Model)
		}
	}
	if calls.count("decider") != 2 {
		t.Fatalf("failing decider was asked %d times, want 2", calls.count("decider"))
	}
	if !strings.Contains(logs.String(), "auto router decision failed") || !strings.Contains(logs.String(), "decider down") {
		t.Fatalf("failed decision was not logged: %s", logs.String())
	}

	failing.Store(false)
	time.Sleep(500 * time.Millisecond)
	response := postReplayResponse(t, url, "auto", userInput("Write poem 9."), nil, false)
	if response.Model != e2eClaude || calls.count("decider") != 3 {
		t.Fatalf("recovered decider was not used: %s after %d decisions", response.Model, calls.count("decider"))
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The decider sees the start and the end of a long request, and only the
// evidence that applies.
func TestAutoRouterDecisionEvidence(t *testing.T) {
	var mu sync.Mutex
	var decisions []decisionRequest
	script := func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		mu.Lock()
		defer mu.Unlock()
		switch call.Path {
		case "/v1/decisions":
			decisions = append(decisions, poemsToClaude(t, w, call))
		case "/v1/responses":
			writeOpenAIText(w, "From GPT")
		case "/v1/messages":
			writeAnthropicText(w, "From Claude")
		}
	}
	last := func() decisionRequest {
		mu.Lock()
		defer mu.Unlock()
		if len(decisions) == 0 {
			t.Fatal("the decider was not asked")
		}
		return decisions[len(decisions)-1]
	}

	t.Run("long request", func(t *testing.T) {
		_, url := routerE2E(t, "", e2eAutoRouter, script)
		const question = "Write a poem that proves this distributed algorithm is linearizable."
		document := "DOCUMENT START " + strings.Repeat("lorem ipsum dolor sit amet ", 900)
		input := []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "input_text", "text": document},
			{"type": "input_text", "text": question},
		}}}
		if response := postReplayResponse(t, url, "auto", input, nil, false); response.Model != e2eClaude {
			t.Fatalf("the question did not decide the route: %s", response.Model)
		}
		request := last().Request
		if len(document) < 20000 || len(request) > 16*1024 || !strings.HasPrefix(request, "DOCUMENT START") || !strings.HasSuffix(request, question) || !strings.Contains(request, "[...]") {
			t.Fatalf("excerpt of %d bytes ends with %q", len(request), request[max(0, len(request)-80):])
		}
	})

	t.Run("nothing to report", func(t *testing.T) {
		_, url := routerE2E(t, "", e2eAutoRouter, script)
		postReplayResponse(t, url, "auto", userInput("Write a poem."), nil, false)
		for _, absent := range []string{"tool_results_since_request", "failed_tool_results_since_request", "tools_available"} {
			if strings.Contains(last().Input, absent) {
				t.Errorf("evidence carries an empty %s: %s", absent, last().Input)
			}
		}
	})

	t.Run("tool results", func(t *testing.T) {
		_, url := routerE2E(t, "", e2eAutoRouter, script)
		input := []map[string]any{
			{"role": "user", "content": "Write a poem using glob."},
			{"type": "function_call", "call_id": "call_1", "name": "glob", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "three files"},
		}
		postReplayResponse(t, url, "auto", input, geminiReplayTools(), false)
		for _, present := range []string{`"tool_results_since_request":1`, `"tools_available":true`} {
			if !strings.Contains(last().Input, present) {
				t.Errorf("evidence lacks %s: %s", present, last().Input)
			}
		}
	})
}

// Routers resolve one another whatever their order in the configuration: a
// load balancer falls back on an auto router, which routes to another one.
func TestRoutersReferenceEachOtherInAnyOrder(t *testing.T) {
	routers := `
routers:
  pool:
    type: roundrobin
    models: [gpt-6.2-sol]
    fallback: outer
  outer:
    type: auto
    decider: gpt-6-luna
    routes:
      - model: inner
        when: The latest request asks for poetry or creative writing.
      - model: gpt-6.2-astra
        when: The latest request asks for arithmetic or a number.
  inner:
    type: auto
    decider: gpt-6-luna
    routes:
      - model: claude-sonnet-5-5
        when: The latest request asks for a short poem.
      - model: claude-opus-5-5
        when: The latest request asks for a long poem.
`
	var calls upstreamLog
	_, url := routerE2E(t, "", routers, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		switch call.Path {
		case "/v1/decisions":
			calls.add("decider")
			decision := parseDecision(t, call.Body)
			selected := e2eClaude
			if slices.Contains(decision.Choices, "inner") {
				selected = "inner"
			}
			writeDecision(w, selected, decision.Choices)
		case "/v1/responses":
			calls.add(call.Model)
			writeOpenAIError(w, 503, "server_error", "overloaded")
		case "/v1/messages":
			calls.add(call.Model)
			writeAnthropicText(w, "From Claude")
		}
	})
	response := postReplayResponse(t, url, "pool", userInput("Write a short poem."), nil, false)
	if response.Model != e2eClaude || outputText(response) != "From Claude" || calls.count("decider") != 2 || calls.count(e2eGPT) != 1 || calls.count(e2eClaude) != 1 {
		t.Fatalf("%s answered %q after %d decisions", response.Model, outputText(response), calls.count("decider"))
	}
}

// An agent in front of a router reports the model that served the request,
// not the router. An agent in front of a model keeps reporting its alias.
func TestAgentReportsModelBehindRouter(t *testing.T) {
	agents := e2eAutoRouter + `
agents:
  poet:
    type: assistant
    model: auto
    messages:
      - role: system
        content: You are a poet.
  worker:
    type: react
    model: auto
  plain:
    type: assistant
    model: gpt-6.2-sol
`
	eachStreamMode(t, func(t *testing.T, stream bool) {
		_, url := routerE2E(t, "", agents, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
			switch call.Path {
			case "/v1/decisions":
				poemsToClaude(t, w, call)
			case "/v1/responses":
				// OpenAI reports the dated snapshot behind an alias.
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"model":"gpt-6.2-sol-2026-11-01","status":"completed","output":[{"type":"message","id":"msg_gpt","role":"assistant","content":[{"type":"output_text","text":"The number is 17."}]}]}}`+"\n\n")
			case "/v1/messages":
				writeAnthropicText(w, "A short verse")
			}
		})
		for _, tc := range []struct{ agent, prompt, model string }{
			{"poet", "Write a poem.", e2eClaude},
			{"poet", "Give me a number.", "gpt-6.2-sol-2026-11-01"},
			{"worker", "Write a poem.", e2eClaude},
			{"worker", "Give me a number.", "gpt-6.2-sol-2026-11-01"},
			{"plain", "Give me a number.", e2eGPT},
		} {
			if response := postReplayResponse(t, url, tc.agent, userInput(tc.prompt), nil, stream); response.Model != tc.model {
				t.Errorf("%s answering %q reports %s, want %s", tc.agent, tc.prompt, response.Model, tc.model)
			}
		}
	})
}
