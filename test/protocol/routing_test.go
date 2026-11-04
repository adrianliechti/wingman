package protocol_test

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/mcp/proxy"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/test/webtest"
)

func TestAuthenticationOnPublicRoutes(t *testing.T) {
	authorizer, err := static.New("contract-secret")
	if err != nil {
		t.Fatal(err)
	}
	local := configuredGateway(t, &config.Config{Policy: noop.New(), Authorizers: []auth.Provider{authorizer}})
	routes := map[string][]string{
		http.MethodGet: {"/v1/models", "/v1/models/model", "/v1/realtime?model=model", "/v1/token", "/v1/mcp", "/v1/mcp/test/icon", "/v1/mcp/test/events"},
		http.MethodPost: {
			"/v1/chat/completions", "/v1/responses", "/v1/responses/input_tokens", "/v1/decisions", "/v1/embeddings",
			"/v1/audio/speech", "/v1/audio/transcriptions", "/v1/images/generations", "/v1/images/edits",
			"/v1/messages", "/v1/messages/count_tokens", "/v1beta/models/model:generateContent", "/v1beta/models/model:streamGenerateContent", "/v1beta/models/model:countTokens",
			"/v1/exa/search", "/v1/exa/contents", "/v1/tavily/search", "/v1/tavily/extract", "/v1/mcp/test",
			"/v1/extract", "/v1/render", "/v1/search", "/v1/research", "/v1/retrieve", "/v1/rerank", "/v1/systemone", "/v1/segment", "/v1/guard", "/v1/summarize", "/v1/translate", "/v1/transcribe",
		},
	}
	for method, paths := range routes {
		for _, path := range paths {
			t.Run(method+path, func(t *testing.T) {
				for _, token := range []string{"", "wrong-secret"} {
					req, err := http.NewRequestWithContext(t.Context(), method, local.URL+path, strings.NewReader(`{}`))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					resp, err := local.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					requireStatus(t, resp.StatusCode, 401, raw)
					requireType(t, resp.Header, "application/json")
					body := object(t, raw)
					switch {
					case strings.HasPrefix(path, "/v1beta/"):
						equal(t, "status", at(body, "error", "status"), "UNAUTHENTICATED")
					case strings.HasPrefix(path, "/v1/messages"):
						equal(t, "error type", at(body, "error", "type"), "authentication_error")
					case strings.HasPrefix(path, "/v1/exa/"):
						equal(t, "tag", body["tag"], "UNAUTHORIZED")
					case strings.HasPrefix(path, "/v1/tavily/"):
						if at(body, "detail", "error") == nil {
							t.Fatalf("missing Tavily error: %s", raw)
						}
					default:
						equal(t, "error code", at(body, "error", "code"), "invalid_api_key")
					}
				}
			})
		}
	}
	// A successful authenticated request proves the middleware is not simply
	// rejecting every request in the negative matrix.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, local.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer contract-secret")
	resp, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	equal(t, "authenticated status", resp.StatusCode, 200)
}

type modelPolicy struct{}

func (modelPolicy) Verify(_ context.Context, _ policy.Resource, id string, _ policy.Action) error {
	if id == "hidden" {
		return policy.ErrAccessDenied
	}
	return nil
}

func TestModelsRespectPolicy(t *testing.T) {
	cfg := &config.Config{Policy: modelPolicy{}}
	cfg.RegisterModel("visible")
	cfg.RegisterModel("hidden")
	local := configuredGateway(t, cfg)
	status, _, raw := request(t, local, http.MethodGet, "/v1/models", "", nil)
	requireStatus(t, status, 200, raw)
	body := object(t, raw)
	models, ok := body["data"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("model listing leaked inaccessible models: %s", raw)
	}
	equal(t, "visible model", at(models[0], "id"), "visible")
	for _, id := range []string{"visible", "hidden", "unknown"} {
		status, _, raw := request(t, local, http.MethodGet, "/v1/models/"+id, "", nil)
		if id == "visible" {
			requireStatus(t, status, 200, raw)
			equal(t, "model id", object(t, raw)["id"], id)
		} else {
			requireStatus(t, status, 404, raw)
		}
	}
}

func TestTokenCountRoutesStayLocal(t *testing.T) {
	local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("token count called upstream")
		http.Error(w, "unexpected upstream", 500)
	})
	for _, p := range protocols(false)[1:] {
		t.Run(p.name, func(t *testing.T) {
			key := "input_tokens"
			switch p.name {
			case "responses":
				p.path = "/v1/responses/input_tokens"
			case "anthropic":
				p.path = "/v1/messages/count_tokens"
			case "gemini":
				p.path = "/v1beta/models/contract-model:countTokens"
				key = "totalTokens"
			}
			status, headers, raw := post(t, local, p.path, p.body)
			requireStatus(t, status, 200, raw)
			requireType(t, headers, "application/json")
			count, ok := object(t, raw)[key].(float64)
			if !ok || count <= 0 {
				t.Fatalf("missing positive token estimate: %s", raw)
			}
		})
	}
}

func TestGenerationValidationStaysLocal(t *testing.T) {
	local := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid request called upstream")
		http.Error(w, "unexpected upstream", 500)
	})
	for _, p := range protocols(false) {
		t.Run(p.name, func(t *testing.T) {
			status, _, raw := request(t, local, http.MethodPost, p.path, "application/json", strings.NewReader(`{"broken":`))
			requireStatus(t, status, 400, raw)
			if at(object(t, raw), "error", "message") == nil {
				t.Fatalf("missing protocol error: %s", raw)
			}
			p.body["model"] = "unknown"
			p.path = strings.ReplaceAll(p.path, "contract-model", "unknown")
			status, _, raw = post(t, local, p.path, p.body)
			requireStatus(t, status, 404, raw)
		})
	}
}

func TestSearchProtocolsOverHTTP(t *testing.T) {
	s, _ := webtest.NewServer(t, false)
	local := httptest.NewServer(s)
	t.Cleanup(local.Close)
	for _, tc := range []struct{ path, body, key string }{
		{"/v1/exa/search", `{"model":"plain","query":"kite","contents":{"text":true}}`, "text"},
		{"/v1/exa/contents", `{"model":"plain","ids":["https://example.com"]}`, "text"},
		{"/v1/tavily/search", `{"model":"plain","query":"kite","include_raw_content":true}`, "raw_content"},
		{"/v1/tavily/extract", `{"model":"plain","urls":["https://example.com"]}`, "raw_content"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, local.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer wingman-test-key")
			req.Header.Set("Content-Type", "application/json")
			resp, err := local.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			requireStatus(t, resp.StatusCode, 200, raw)
			body := object(t, raw)
			equal(t, "result URL", at(body, "results", 0, "url"), "https://example.com")
			equal(t, "result content", at(body, "results", 0, tc.key), "Grüezi 世界 — full page")
		})
	}
}

func TestMCPProxyRoundTrip(t *testing.T) {
	wire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" || r.Header.Get("Mcp-Session-Id") != "session-contract" {
			t.Errorf("lost MCP path/session: %s %v", r.URL, r.Header)
		}
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
			return
		}
		if body["method"] != "tools/list" || body["id"] != float64(42) {
			t.Errorf("lost JSON-RPC request: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-contract")
		io.WriteString(w, `{"jsonrpc":"2.0","id":42,"result":{"tools":[]}}`)
	}))
	t.Cleanup(wire.Close)
	backend, err := proxy.New(wire.URL+"/rpc", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterMCP("contract", backend)
	local := configuredGateway(t, cfg)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, local.URL+"/v1/mcp/contract", strings.NewReader(`{"jsonrpc":"2.0","id":42,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "session-contract")
	resp, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, resp.StatusCode, 200, raw)
	equal(t, "session", resp.Header.Get("Mcp-Session-Id"), "session-contract")
	equal(t, "JSON-RPC id", object(t, raw)["id"], float64(42))
}
