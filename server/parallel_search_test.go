package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParallelWebSearch(t *testing.T) {
	const source = "https://docs.example.org/topic?a=1&b=2#section"
	const query = "find the topic documentation"
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	type arguments struct {
		Objective string   `json:"objective"`
		Queries   []string `json:"search_queries"`
	}
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "web_search"}, func(_ context.Context, _ *mcp.CallToolRequest, args arguments) (*mcp.CallToolResult, map[string]any, error) {
		if args.Objective == "upstream failure" {
			failure := &mcp.CallToolResult{}
			failure.SetError(io.ErrUnexpectedEOF)
			return failure, nil, nil
		}
		if args.Objective != query+"\nExclude results from these domains: blocked.example.org" || !slices.Equal(args.Queries, []string{query + " site:example.org"}) {
			t.Errorf("unexpected search arguments: %+v", args)
		}
		return nil, map[string]any{"results": []map[string]any{
			{"url": "javascript:alert(1)", "title": "Invalid source"},
			{"url": "https://example.org.other.test/", "title": "Outside allowlist"},
			{"url": "https://blocked.example.org/", "title": "Blocked source"},
			{"url": source, "title": "Original source", "excerpts": []string{"Relevant excerpt"}, "publish_date": "2026-01-02"},
			{"url": "https://example.org/second", "title": "Beyond result limit"},
		}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	var discoveries, calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.URL.RawQuery != "" || r.UserAgent() != "wingman" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("Parallel request must use /mcp and the wingman User-Agent without credentials")
		}
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			var message struct{ Method string }
			if err != nil || json.Unmarshal(body, &message) != nil {
				t.Error("invalid MCP request body")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			switch message.Method {
			case "server/discover":
				discoveries.Add(1)
			case "tools/call":
				calls.Add(1)
				t.Logf("received %s on %s with User-Agent %q and no credentials", message.Method, r.URL.Path, r.UserAgent())
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	fixture, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	var defaultCalls atomic.Int32
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: parallelFixtureTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "mcp.parallel.ai":
			req := r.Clone(r.Context())
			req.URL.Scheme, req.URL.Host, req.Host = fixture.Scheme, fixture.Host, fixture.Host
			return upstream.Client().Transport.RoundTrip(req)
		case "html.duckduckgo.com":
			defaultCalls.Add(1)
			body := `<div class="result"><a class="result__a" href="https://example.com/default">Default result</a><a class="result__snippet">Default excerpt</a></div>`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		default:
			return nil, io.ErrUnexpectedEOF
		}
	})}
	t.Cleanup(func() { http.DefaultClient = previous })

	configPath := filepath.Join(t.TempDir(), "wingman.yaml")
	if err := os.WriteFile(configPath, []byte(`
authorizers:
  - type: static
    token: test-local-token
searchers:
  default: {type: duckduckgo}
  parallel: {type: parallel}
tools:
  search: {type: search, searcher: parallel}
mcps:
  internet: {type: server, name: internet, tools: [search]}
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.Close() })
	wingman, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(wingman)
	defer app.Close()

	request := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"query":"default search"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-local-token")
	response := httptest.NewRecorder()
	wingman.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "https://example.com/default") || defaultCalls.Load() != 1 {
		t.Fatalf("default search = %d %s, calls = %d", response.Code, response.Body, defaultCalls.Load())
	}

	response = httptest.NewRecorder()
	wingman.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/mcp/internet", nil))
	if response.Code != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatalf("unauthenticated MCP = %d, upstream calls = %d", response.Code, calls.Load())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: app.URL + "/v1/mcp/internet",
		HTTPClient: &http.Client{Transport: parallelFixtureTransport(func(r *http.Request) (*http.Response, error) {
			req := r.Clone(r.Context())
			req.Header.Set("Authorization", "Bearer test-local-token")
			return app.Client().Transport.RoundTrip(req)
		})},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "web_search" {
		t.Fatalf("unexpected tools: %+v", listed.Tools)
	}
	schema := listed.Tools[0].InputSchema.(map[string]any)
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["location"]; ok {
		t.Error("Parallel web_search advertised an unsupported location filter")
	}

	call := func(args map[string]any) (*mcp.CallToolResult, string) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) != 1 {
			t.Fatalf("unexpected tool content: %+v", result.Content)
		}
		return result, result.Content[0].(*mcp.TextContent).Text
	}
	for range 2 {
		result, text := call(map[string]any{
			"query": query, "allowed_domains": []string{"example.org"}, "blocked_domains": []string{"blocked.example.org"}, "max_results": 1,
		})
		if result.IsError || !strings.Contains(text, "Found 1 result(s)") || !strings.Contains(text, "[Original source]("+source+")") || !strings.Contains(text, "Relevant excerpt") || !strings.Contains(text, "2026-01-02") {
			t.Fatalf("unexpected search result: %s", text)
		}
	}
	result, message := call(map[string]any{"query": "upstream failure"})
	if !result.IsError || !strings.Contains(message, "unexpected EOF") {
		t.Fatalf("unexpected failure result: %s", message)
	}
	result, message = call(map[string]any{"query": query, "location": "US"})
	if !result.IsError || !strings.Contains(message, "location is not supported") {
		t.Fatalf("unexpected result for unsupported location: %s", message)
	}
	if got := discoveries.Load(); got != 3 || calls.Load() != 3 {
		t.Fatalf("expected a new upstream session for every call, got %d discoveries and %d calls", got, calls.Load())
	}
	t.Logf("verified %d separate upstream sessions", discoveries.Load())
}

type parallelFixtureTransport func(*http.Request) (*http.Response, error)

func (f parallelFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
