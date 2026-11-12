package server_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server"
)

func TestGeminiDecisionsAcrossAPIs(t *testing.T) {
	for _, kind := range []string{"gemini", "google"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1beta/decisions" || r.Header.Get("x-goog-api-key") != "test-key" {
					t.Errorf("unexpected upstream request: %s %v", r.URL, r.Header)
				}
				var req struct {
					Model     string                    `json:"model"`
					Input     jsontext.Value            `json:"input"`
					Questions map[string]jsontext.Value `json:"questions"`
				}
				if err := json.UnmarshalRead(r.Body, &req); err != nil {
					t.Error(err)
				}
				if req.Model != "gemini-decision-1" || len(req.Questions) != 1 || !strings.Contains(string(req.Input), "cracked") {
					t.Errorf("incorrect request: %+v", req)
				}
				for id := range req.Questions {
					fmt.Fprintf(w, `{"answers":{%q:{"type":"noul","noul":0.99,"confidence":0.99}},"usage":{"total_input_tokens":100,"total_output_tokens":0,"total_tokens":100}}`, id)
				}
			}))
			defer upstream.Close()
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`providers:
  - type: %s
    url: %s/v1beta
    token: test-key
    max_retries: 0
    models:
      damage-check:
        id: gemini-decision-1
`, kind, upstream.URL)), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.Completer("damage-check"); err == nil {
				t.Fatal("decision model registered as completer")
			}
			if len(cfg.Models()) != 1 || cfg.Models()[0].ID != "damage-check" {
				t.Fatalf("missing model alias: %+v", cfg.Models())
			}
			access := &decisionPolicy{}
			cfg.Policy = access
			s, err := server.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []struct{ path, body, want string }{
				{"/v1beta/decisions", `"input":"arrived cracked","questions":{"has_damage":{"type":"noul","instructions":"Damaged?"}}`, `"total_input_tokens":100`},
				{"/v1/systemone", `"state":"arrived cracked","questions":{"has_damage":{"type":"noul","instructions":"Damaged?"}}`, `"input_tokens":100`},
				{"/v1/decisions", `"input":"arrived cracked","questions":[{"name":"has_damage","type":"predicate","instructions":"Damaged?"}]`, `"input_tokens":100`},
			} {
				for _, model := range []string{`"model":"damage-check",`, ""} {
					w := httptest.NewRecorder()
					s.ServeHTTP(w, httptest.NewRequest("POST", endpoint.path, strings.NewReader("{"+model+endpoint.body+"}")))
					if w.Code != 200 || !strings.Contains(w.Body.String(), endpoint.want) {
						t.Fatalf("%s status=%d body=%s", endpoint.path, w.Code, w.Body.String())
					}
					if access.seen != "model/damage-check/access" {
						t.Fatalf("wrong policy alias: %q", access.seen)
					}
				}
			}
			if calls != 6 {
				t.Fatalf("expected one native call per request, got %d", calls)
			}
			access.err = policy.ErrAccessDenied
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/decisions", strings.NewReader(`{"input":"cracked","questions":{"has_damage":{"type":"noul","instructions":"Damaged?"}}}`)))
			if w.Code != 404 || calls != 6 {
				t.Fatalf("denied request reached backend: %d %d", w.Code, calls)
			}
		})
	}
}

func TestGeminiDecisionsAdaptersAndErrors(t *testing.T) {
	backend := &decisionCompleter{output: `{"q0":{"0":1,"1":0}}`}
	_, s := decisionsServer(t, backend)
	body := `{"model":"chat","input":{"type":"text","text":"hello"},"questions":{"has_damage":{"type":"noul","instructions":"Damaged?"}}}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/decisions", strings.NewReader(body)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"noul":1`) || !strings.Contains(w.Body.String(), `"total_output_tokens":20`) {
		t.Fatalf("adapter response: %d %s", w.Code, w.Body.String())
	}
	if backend.messages[len(backend.messages)-2].Content[0].Text != "hello" {
		t.Fatal("native text block lost on fallback")
	}
	// Exercise Google error formatting and Retry-After with the existing adapter.
	backend.err = &provider.ProviderError{Code: 429, Message: "busy", RetryAfter: 2 * time.Second}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/decisions", strings.NewReader(body)))
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), `"status":"RESOURCE_EXHAUSTED"`) {
		t.Fatalf("error response: %d %s", w.Code, w.Body.String())
	}
	backend.err = &provider.ProviderError{Code: 400, Type: "safety", Message: "blocked"}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/decisions", strings.NewReader(body)))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"safety"`) {
		t.Fatalf("safety error code lost: %d %s", w.Code, w.Body.String())
	}
	calls := backend.calls
	for _, request := range []string{
		`{"model":"chat","input":"hello","questions":{"bad.key":{"type":"noul","instructions":"Question?"}}}`,
		`{"model":"missing","input":"hello","questions":{"q":{"type":"noul","instructions":"Question?"}}}`,
		`{"input":"hello","questions":{"q":{"type":"score","instructions":"Question?","criteria":["one"]}}}`,
	} {
		w = httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/decisions", strings.NewReader(request)))
		if w.Code != 400 || backend.calls != calls {
			t.Fatalf("invalid request reached backend: %d %s", w.Code, w.Body.String())
		}
	}
}
