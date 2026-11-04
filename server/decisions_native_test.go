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
	"github.com/adrianliechti/wingman/server"
)

func TestNativeDecisionsRoutes(t *testing.T) {
	upstreamCalls := 0
	var wantPath, wantModel, wantState string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != wantPath || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model     string         `json:"model"`
			State     jsontext.Value `json:"state"`
			Questions map[string]struct {
				Type         string         `json:"type"`
				Instructions jsontext.Value `json:"instructions"`
				Criteria     jsontext.Value `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if body.Model != wantModel || string(body.State) != wantState || len(body.Questions) != 3 ||
			string(body.Questions["score"].Criteria) != `["Low",{"id":9007199254740993,"label":"High"}]` ||
			string(body.Questions["noul"].Instructions) != `{"ask":"Urgent?"}` {
			t.Errorf("upstream request lost typed input: %+v", body)
		}
		io.WriteString(w, `{
			"id":"native-id","model":"actual-native-model","answers":{
				"noul":{"type":"noul","noul":0},
				"choice":{"type":"choice","choice":"bug","probabilities":{"bug":1,"billing":0},"confidence":0.892},
				"score":{"type":"score","score":0,"probabilities":{"0":1,"1":0},"confidence":0,
					"legend":{"0":"Low","1":{"label":"High","id":9007199254740993}}}
			},"usage":{"input_tokens":168,"output_tokens":1}
		}`)
	}))
	defer upstream.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	data := fmt.Sprintf(`
providers:
  - type: typesafe
    url: %s/v1/systemone
    token: test-key
    models:
      nimble:
        id: nimble
  - type: typesafe
    url: %s/v1/systemone
    token: test-key
    models:
      - jev-latest
  - type: typesafe
    url: %s/api/alpha/decisions
    token: test-key
    models:
      jev-1.13:
        id: typesafe/jev-1.13
`, upstream.URL, upstream.URL, upstream.URL)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models()) != 3 {
		t.Fatalf("native models not advertised: %+v", cfg.Models())
	}
	accessPolicy := &decisionPolicy{}
	cfg.Policy = accessPolicy
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []struct{ alias, upstream, path string }{
		{"nimble", "nimble", "/v1/systemone"},
		{"jev-latest", "jev-latest", "/v1/systemone"},
		{"jev-1.13", "typesafe/jev-1.13", "/api/alpha/decisions"},
	} {
		t.Run(model.alias, func(t *testing.T) {
			if _, err := cfg.Completer(model.alias); err == nil {
				t.Fatal("decision-only model was registered as a completer")
			}
			wantModel, wantPath = model.upstream, model.path
			for _, state := range []string{`"checkout failed"`, `{"text":"checkout failed"}`, `["checkout failed",9007199254740993]`, `null`} {
				wantState = state
				body := fmt.Sprintf(`{"model":%q,"state":%s,"questions":{
					"noul":{"type":"noul","instructions":{"ask":"Urgent?"},"criteria":{"true":"Act now"}},
					"choice":{"type":"choice","instructions":"Which label?","criteria":{"bug":null,"billing":null}},
					"score":{"type":"score","instructions":["Severity?"],"criteria":["Low",{"label":"High","id":9007199254740993}]}
				}}`, model.alias, state)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				var result struct {
					ID      string                               `json:"id"`
					Model   string                               `json:"model"`
					Answers map[string]map[string]jsontext.Value `json:"answers"`
					Usage   map[string]int                       `json:"usage"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.ID != "native-id" || result.Model != "actual-native-model" || result.Usage["input_tokens"] != 168 || result.Usage["output_tokens"] != 1 ||
					string(result.Answers["choice"]["confidence"]) != "0.892" ||
					string(result.Answers["noul"]["noul"]) != "0" || string(result.Answers["score"]["score"]) != "0" ||
					string(result.Answers["score"]["confidence"]) != "0" || !strings.Contains(string(result.Answers["score"]["legend"]), "9007199254740993") {
					t.Fatalf("upstream response changed: %s", w.Body.String())
				}
				if accessPolicy.seen != "model/"+model.alias+"/access" {
					t.Fatalf("policy checked %q", accessPolicy.seen)
				}
			}
		})
	}

	calls := upstreamCalls
	accessPolicy.err = policy.ErrAccessDenied
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"nimble","state":"hello","questions":{"q":{"type":"noul"}}}`)))
	if w.Code != http.StatusNotFound || upstreamCalls != calls {
		t.Fatalf("denied request reached native model: status %d, calls %d", w.Code, upstreamCalls)
	}
}
