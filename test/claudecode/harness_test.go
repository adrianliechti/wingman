package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/test/harness"
)

type exchange struct {
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	RequestHeaders  http.Header `json:"request_headers"`
	Request         string      `json:"request"`
	Status          int         `json:"status"`
	ResponseHeaders http.Header `json:"response_headers"`
	Response        string      `json:"response"`
}

type recorder struct {
	*httptest.Server
	mu        sync.Mutex
	exchanges []exchange
}

// Claude receives a dummy credential. Only this proxy knows the upstream key,
// and only protocol headers are recorded in artifacts.
func newRecorder(t *testing.T, endpoint harness.Endpoint) *recorder {
	t.Helper()
	u, err := url.Parse(endpoint.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		t.Fatal("endpoint must be an HTTP(S) base URL without credentials, query or fragment")
	}
	// The existing API harness uses /v1 base URLs; Claude appends /v1 itself.
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1")
	u.RawPath = ""
	r := &recorder{}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(u)
			p.Out.Header.Del("Accept-Encoding") // Let Go decode compressed responses before recording.
			p.Out.Header.Del("Authorization")
			p.Out.Header.Del("X-Api-Key")
			p.Out.Header.Set("X-Api-Key", endpoint.APIKey)
			if endpoint.Name == "wingman" {
				p.Out.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			http.Error(w, "upstream request failed: "+err.Error(), http.StatusBadGateway)
		},
	}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		record := exchange{Method: req.Method, Path: req.URL.RequestURI(), RequestHeaders: protocolHeaders(req.Header), Request: string(body)}
		cw := &captureWriter{ResponseWriter: w}
		defer func() {
			// Keep partial responses even when ReverseProxy aborts a broken stream.
			record.Status, record.Response = cw.status, cw.body.String()
			record.ResponseHeaders = protocolHeaders(w.Header())
			r.mu.Lock()
			r.exchanges = append(r.exchanges, record)
			r.mu.Unlock()
		}()
		proxy.ServeHTTP(cw, req)
	}))
	t.Cleanup(r.Close)
	return r
}

func protocolHeaders(headers http.Header) http.Header {
	result := http.Header{}
	for _, key := range []string{"Content-Type", "Anthropic-Version", "Anthropic-Beta", "User-Agent", "Request-Id"} {
		if values := headers.Values(key); len(values) > 0 {
			result[key] = append([]string(nil), values...)
		}
	}
	return result
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.body.Write(p[:n])
	return n, err
}

// ReverseProxy uses ResponseController to flush through this wrapper.
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type cliEvent struct {
	Type              string            `json:"type"`
	Subtype           string            `json:"subtype"`
	IsError           bool              `json:"is_error"`
	Result            string            `json:"result"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
}

func claudeEnv(configDir, baseURL, model string) []string {
	var env []string
	// Keep the runtime environment, but do not inherit provider credentials,
	// Claude settings, telemetry exporters or model overrides from the caller.
	for _, key := range []string{"PATH", "HOME", "USER", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+baseURL,
		"ANTHROPIC_API_KEY=wingman-claude-code-test",
		"ANTHROPIC_MODEL="+model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL="+model,
		"ANTHROPIC_DEFAULT_OPUS_MODEL="+model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL="+model,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_MAX_RETRIES=0",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=2048",
		"API_TIMEOUT_MS=90000",
	)
}

func runClaude(t *testing.T, binary string, endpoint harness.Endpoint, model, prompt, tools, input string) (string, []exchange, string) {
	t.Helper()
	dir := t.TempDir()
	if root := os.Getenv("CLAUDE_CODE_ARTIFACTS"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(root, strings.ReplaceAll(t.Name(), "/", "-")+"-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture")
	configDir := filepath.Join(dir, "config")
	for _, path := range []string{fixture, configDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if input != "" {
		writeArtifact(t, filepath.Join(fixture, "input.txt"), []byte(input))
		writeArtifact(t, filepath.Join(fixture, "output.txt"), []byte("REPLACE_ME\n"))
		prompt += fmt.Sprintf("\nUse the absolute paths %q and %q. For Read, pass only file_path; these are plain text files.", filepath.Join(fixture, "input.txt"), filepath.Join(fixture, "output.txt"))
	}
	r := newRecorder(t, endpoint)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary,
		"--bare", "--print", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--no-session-persistence", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands", "--permission-mode", "dontAsk", "--tools", tools, "--allowedTools", tools,
		"--model", model, "--effort", "low", "--max-turns", "6", "--max-budget-usd", "1",
		"--", prompt,
	)
	cmd.Dir, cmd.Env, cmd.WaitDelay = fixture, claudeEnv(configDir, r.URL, model), 5*time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	r.Close() // Drain handlers before reading the captured exchanges.
	writeArtifact(t, filepath.Join(dir, "stdout.jsonl"), stdout.Bytes())
	writeArtifact(t, filepath.Join(dir, "stderr.log"), stderr.Bytes())
	data, err := json.MarshalIndent(r.exchanges, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, filepath.Join(dir, "http.json"), data)
	t.Logf("%s/%s: %d HTTP exchanges; artifacts: %s", endpoint.Name, model, len(r.exchanges), dir)
	if runErr != nil {
		t.Errorf("Claude Code failed: %v (context: %v)\n%s", runErr, ctx.Err(), stderr.String())
	}
	decoder := json.NewDecoder(&stdout)
	var result *cliEvent
	for {
		var event cliEvent
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("invalid Claude Code JSON output: %v", err)
		}
		if event.Type == "result" {
			if result != nil {
				t.Error("multiple final results from Claude Code")
			}
			result = &event
		}
	}
	if result == nil {
		t.Fatalf("Claude Code emitted no final result; stderr: %s", stderr.String())
	}
	if result.IsError || result.Subtype != "success" || len(result.PermissionDenials) != 0 {
		t.Fatalf("Claude Code did not complete successfully: %+v", *result)
	}
	return strings.TrimSpace(result.Result), r.exchanges, fixture
}

func writeArtifact(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(fmt.Errorf("write artifact: %w", err))
	}
}
