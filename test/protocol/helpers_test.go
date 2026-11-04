package protocol_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"
	"github.com/adrianliechti/wingman/test/harness"
)

const answer = "Grüezi 🌍 — 你好"

func gateway(t *testing.T, upstream http.HandlerFunc) *httptest.Server {
	t.Helper()
	wire := httptest.NewServer(upstream)
	t.Cleanup(wire.Close)
	cfg := &config.Config{Policy: noop.New()}
	backend, err := openai.NewCompleter(wire.URL, "gpt-4o", openai.WithToken("synthetic"), openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterCompleter("contract-model", backend)
	return configuredGateway(t, cfg)
}

func configuredGateway(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(s)
	local.Client().Timeout = 10 * time.Second
	t.Cleanup(local.Close)
	return local
}

func request(t *testing.T, local *httptest.Server, method, path, contentType string, body io.Reader) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, local.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, raw
}

func post(t *testing.T, local *httptest.Server, path string, body any) (int, http.Header, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return request(t, local, http.MethodPost, path, "application/json", bytes.NewReader(raw))
}

func object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, raw)
	}
	if result == nil {
		t.Fatalf("expected JSON object: %s", raw)
	}
	return result
}

func at(value any, path ...any) any {
	for _, key := range path {
		switch key := key.(type) {
		case string:
			m, _ := value.(map[string]any)
			value = m[key]
		case int:
			a, _ := value.([]any)
			if key >= len(a) {
				return nil
			}
			value = a[key]
		}
	}
	return value
}

func equal(t *testing.T, label string, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %#v, want %#v", label, got, want)
	}
}

func frames(t *testing.T, raw []byte) []*harness.SSEEvent {
	t.Helper()
	events, err := harness.ParseSSE(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("no SSE events: %s", raw)
	}
	for _, e := range events {
		if e.Raw != "[DONE]" && e.Data == nil {
			t.Fatalf("invalid SSE data: %q", e.Raw)
		}
	}
	return events
}

func writeChunk(w http.ResponseWriter, delta any, finish any, usage any) {
	chunk := map[string]any{"id": "chatcmpl-contract", "object": "chat.completion.chunk", "model": "gpt-4o", "created": 1, "choices": []any{}}
	if delta != nil {
		chunk["choices"] = []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	raw, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", raw)
	http.NewResponseController(w).Flush()
}

func textUpstream(t *testing.T, mode string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected path", 500)
			return
		}
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
			return
		}
		if body["stream"] != true {
			t.Errorf("upstream must stream: %v", body)
		}
		if mode == "rate_limit" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"contract rate limit","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if mode == "empty" {
			return
		}
		for _, text := range []string{"Grüezi ", "🌍 — ", "你好"} {
			writeChunk(w, map[string]any{"role": "assistant", "content": text}, nil, nil)
		}
		switch mode {
		case "cancel":
			<-r.Context().Done()
			return
		case "truncated":
			return // Clean HTTP EOF without the protocol's terminal choice.
		case "disconnect":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		case "error":
			io.WriteString(w, "data: {\"error\":{\"message\":\"contract upstream failure\",\"type\":\"server_error\",\"code\":\"server_error\"}}\n\n")
			return
		}
		finish := "stop"
		if mode == "length" {
			finish = "length"
		}
		writeChunk(w, map[string]any{}, finish, nil)
		writeChunk(w, nil, nil, map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12})
		io.WriteString(w, "data: [DONE]\n\n")
	}
}

type protocol struct {
	name, path string
	body       map[string]any
}

func protocols(stream bool) []protocol {
	geminiPath := "/v1beta/models/contract-model:generateContent"
	if stream {
		geminiPath = "/v1beta/models/contract-model:streamGenerateContent?alt=sse"
	}
	return []protocol{
		{"chat", "/v1/chat/completions", map[string]any{"model": "contract-model", "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": stream, "stream_options": map[string]any{"include_usage": true}}},
		{"responses", "/v1/responses", map[string]any{"model": "contract-model", "input": "hello", "stream": stream}},
		{"anthropic", "/v1/messages", map[string]any{"model": "contract-model", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": stream}},
		{"gemini", geminiPath, map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}}},
	}
}

func requireStatus(t *testing.T, status, want int, raw []byte) {
	t.Helper()
	if status != want {
		t.Fatalf("HTTP %d, want %d: %s", status, want, raw)
	}
}

func requireType(t *testing.T, headers http.Header, want string) {
	t.Helper()
	if got := headers.Get("Content-Type"); !strings.HasPrefix(got, want) {
		t.Fatalf("Content-Type = %q, want %s", got, want)
	}
}
