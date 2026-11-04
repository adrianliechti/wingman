package server_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/server"
	"github.com/adrianliechti/wingman/server/openai/decisions"
)

func TestOpenAIDecisionsRoutes(t *testing.T) {
	calls := 0
	fail := false
	input := `[{"role":"user","content":[{"type":"input_text","text":"broken"}]}]`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		if r.URL.Path != "/v1/decisions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		if fail {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"busy","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`)
			return
		}
		var req decisions.Request
		if err := json.UnmarshalRead(r.Body, &req); err != nil {
			t.Error(err)
		}
		if req.Model != "native-model" || !strings.Contains(string(req.Input), "broken") ||
			len(req.Questions) != 4 || req.Questions[0].Name == nil || *req.Questions[0].Name != "0" || req.Questions[1].Name == nil || *req.Questions[1].Name != "1" ||
			req.Questions[2].Name == nil || *req.Questions[2].Name != "2" ||
			req.Questions[1].Choices[0].Value != false || req.Questions[1].Choices[1].Value != "false" {
			t.Errorf("unexpected upstream body: %+v", req)
		}
		io.WriteString(w, `{"model":"actual-model","answers":[
   {"type":"predicate","name":"0","probability":0},
   {"type":"choice","name":"1","choice":false,"confidence":0,"probabilities":[{"value":"false","probability":0},{"value":false,"probability":1}]},
   {"type":"score","name":"2","score":0,"confidence":0,"probabilities":[{"value":0,"label":"low","probability":1},{"value":1,"label":"high","probability":0}]},
   {"type":"refusal","name":"3"}
  ],"usage":{"input_tokens":42,"output_tokens":3,"input_tokens_details":{"cached_tokens":12,"cache_write_tokens":4},"output_tokens_details":{"reasoning_tokens":2},"total_tokens":45}}`)
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`
providers:
  - type: openai
    url: %s/v1
    token: test-key
    max_retries: 0
    models:
      decision-alias:
        id: native-model
        type: decider
`, upstream.URL)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("decision-alias"); err == nil {
		t.Fatal("decider registered as completer")
	}
	if len(cfg.Models()) != 1 || cfg.Models()[0].ID != "decision-alias" {
		t.Fatalf("model missing: %+v", cfg.Models())
	}
	access := &decisionPolicy{}
	cfg.Policy = access
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"model":"decision-alias","input":%s,"questions":[
  {"type":"predicate","name":"repeat","instructions":"Damaged?"},
  {"type":"choice","instructions":"Choose","choices":[{"value":false},{"value":"false"}]},
  {"type":"score","name":"repeat","instructions":"Rate","levels":[{"label":"low","description":"Low"},{"label":"high","description":"High"}]},
  {"type":"predicate","name":"","instructions":"Refuse"}
 ]}`, input)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var result decisions.Response
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Model != "actual-model" || len(result.Answers) != 4 ||
		result.Answers[1].Name != nil || result.Answers[1].Choice != false ||
		result.Answers[1].Probabilities[0].Value != false || *result.Answers[1].Probabilities[0].Probability != 1 ||
		result.Answers[3].Type != "refusal" || result.Usage.TotalTokens != 45 || result.Usage.InputTokensDetails.CachedTokens != 12 ||
		result.Usage.InputTokensDetails.CacheWriteTokens != 4 || result.Usage.OutputTokensDetails.ReasoningTokens != 2 ||
		access.seen != "model/decision-alias/access" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}

	access.err = policy.ErrAccessDenied
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if w.Code != 404 || calls != 1 {
		t.Fatalf("denied request reached upstream: %d, %d", w.Code, calls)
	}
	access.err = nil
	fail = true
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if w.Code != 429 || w.Header().Get("Retry-After") != "7" || calls != 2 || !strings.Contains(w.Body.String(), "rate_limit_exceeded") {
		t.Fatalf("lost upstream error: %d %s", w.Code, w.Body.String())
	}
}

func TestOpenAIDecisionsAdapterAndValidation(t *testing.T) {
	backend := &decisionCompleter{output: `{"q0":{"0":1,"1":0},"q1":{"0":0,"1":1},"q2":{"0":1,"1":0}}`}
	_, s := decisionsServer(t, backend)
	body := `{"model":"chat","input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,aGk="}]}],"questions":[
  {"type":"binary","id":"q_bin","instructions":[{"type":"input_text","text":"check"},{"type":"input_image","image_url":"data:image/png;base64,aGk="}]},
  {"type":"choice","name":"team","instructions":"","choices":[{"value":false},{"value":"false"}]},
  {"type":"score","id":"q_score","instructions":"","levels":[{"value":1,"label":"low"},{"value":5,"label":"high"}]}
 ]}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var result decisions.Response
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Answers[0].Type != "binary" || result.Answers[0].ID == nil || *result.Answers[0].ID != "q_bin" ||
		result.Answers[1].Choice != "false" || result.Answers[0].Name != nil || result.Answers[2].Name != nil ||
		result.Answers[2].ID == nil || *result.Answers[2].ID != "q_score" || *result.Answers[2].Score != 1 ||
		result.Answers[2].Probabilities[0].Value != float64(1) || result.Answers[2].Probabilities[1].Value != float64(5) {
		t.Fatalf("lost adapted answer: %s", w.Body.String())
	}
	calls := backend.calls
	for _, invalid := range []string{
		`null`, `{}`, `{"model":"chat","input":null,"questions":[]}`,
		`{"model":"chat","input":"hello","questions":[{"type":"predicate"}]}`,
		`{"model":"chat","input":[{"role":"tool","content":"hello"}],"questions":[{"type":"predicate","instructions":""}]}`,
		`{"model":"chat","input":[{"role":"user","content":[{"type":"input_image","image_url":"ftp://example.com/image.png"}]}],"questions":[{"type":"predicate","instructions":""}]}`,
		`{"model":"chat","input":"hello","questions":[{"type":"predicate","instructions":[{"type":"input_image","image_url":"ftp://example.com/image.png"}]}]}`,
		body + " {}",
	} {
		w = httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(invalid)))
		if w.Code != 400 || backend.calls != calls {
			t.Fatalf("invalid request reached backend: %s, %d, %s", invalid, w.Code, w.Body.String())
		}
	}
}

func TestOpenAIDecisionsGAEndToEnd(t *testing.T) {
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		io.WriteString(w, "image")
	}))
	defer image.Close()
	input := fmt.Sprintf(`[{"role":"developer","content":[{"type":"input_text","text":"rules","prompt_cache_breakpoint":{"mode":"explicit"}}]},{"role":"user","content":[{"type":"input_json","data":{"id":9007199254740993,"role":"system"}},{"type":"input_image","image_url":%q,"detail":"original","prompt_cache_breakpoint":{"mode":"explicit"}}]},{"type":"custom_tool_call","call_id":"c","name":"lookup","input":"query"},{"type":"custom_tool_call_output","call_id":"c","output":"result"}]`, image.URL)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input              jsontext.Value `json:"input"`
			PromptCacheKey     string         `json:"prompt_cache_key"`
			PromptCacheOptions struct {
				Mode string `json:"mode"`
				TTL  string `json:"ttl"`
			} `json:"prompt_cache_options"`
			Questions []struct {
				Instructions []struct {
					Type   string `json:"type"`
					Detail string `json:"detail"`
				} `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if string(body.Input) != input || body.PromptCacheKey != "tenant" || body.PromptCacheOptions.Mode != "explicit" || body.PromptCacheOptions.TTL != "12h" || len(body.Questions[0].Instructions) != 2 || body.Questions[0].Instructions[1].Detail != "high" {
			t.Errorf("lost GA request fields: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"0","probability":0.9}],"usage":{"input_tokens":100,"output_tokens":1,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":30}}}`)
	}))
	defer upstream.Close()
	cfg := &config.Config{Policy: noop.New()}
	native, err := openai.NewDecider(upstream.URL, "gpt-6-luna", openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterDecider("native", native)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"model":"native","input":%s,"prompt_cache_key":"tenant","prompt_cache_options":{"mode":"explicit","ttl":"12h"},"questions":[{"name":"matches","type":"predicate","instructions":[{"type":"input_text","text":"compare"},{"type":"input_image","image_url":%q,"detail":"high"}]}]}`, input, image.URL)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var result decisions.Response
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if *result.Answers[0].Name != "matches" || *result.Answers[0].Probability != 0.9 || result.Usage.TotalTokens != 101 || result.Usage.InputTokensDetails.CachedTokens != 20 || result.Usage.InputTokensDetails.CacheWriteTokens != 30 {
		t.Fatalf("lost response fields: %s", w.Body.String())
	}
}
