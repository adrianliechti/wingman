package server_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/server"
)

// decisionBackend serves "chat" as a completion model and as a decider that
// answers every question with one configured option.
type decisionBackend struct {
	output string
	err    error
	calls  int
	input  *provider.DecisionInput

	noul float64
	pick int
}

func (p *decisionBackend) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	p.calls++
	return func(yield func(*provider.Completion, error) bool) {
		if p.err != nil {
			yield(nil, p.err)
			return
		}
		message := provider.AssistantMessage(p.output)
		yield(&provider.Completion{Model: "actual-model", Message: &message, Usage: &provider.Usage{InputTokens: 50, OutputTokens: 20}}, nil)
	}
}

func (p *decisionBackend) Decide(_ context.Context, input *provider.DecisionInput) (*provider.Decision, error) {
	p.calls++
	p.input = input
	if p.err != nil {
		return nil, p.err
	}
	decision := &provider.Decision{Model: "actual-model", Usage: &provider.Usage{InputTokens: 50, OutputTokens: 20}}
	for _, q := range input.Questions {
		answer := provider.DecisionAnswer{ID: q.ID}
		switch {
		case q.Noul != nil:
			answer.Noul = &provider.NoulAnswer{Probability: p.noul}
		case q.Choice != nil:
			probabilities := map[string]float64{}
			for i, option := range q.Choice.Options {
				probabilities[option.Label] = 0
				if i == p.pick {
					probabilities[option.Label] = 1
				}
			}
			answer.Choice = &provider.ChoiceAnswer{Choice: q.Choice.Options[p.pick].Label, Probabilities: probabilities}
		case q.Score != nil:
			score := &provider.ScoreAnswer{}
			for i, description := range q.Score.Levels {
				level := provider.ScoreLevel{Value: i, Description: description}
				if i < len(q.Score.Values) {
					level.Value = q.Score.Values[i]
				}
				if i < len(q.Score.Labels) {
					level.Label = q.Score.Labels[i]
				}
				if i == p.pick {
					level.Probability = 1
					score.Score = float64(level.Value)
				}
				score.Levels = append(score.Levels, level)
			}
			answer.Score = score
		case q.Box != nil:
			answer.Box = &provider.BoxAnswer{Box: provider.BoxCoordinates{Top: 0.2, Bottom: 0.66, Left: 0.19, Right: 0.68}}
		}
		decision.Answers = append(decision.Answers, answer)
	}
	return decision, nil
}

type decisionPolicy struct {
	seen string
	err  error
}

func (p *decisionPolicy) Verify(ctx context.Context, resource policy.Resource, id string, action policy.Action) error {
	p.seen = string(resource) + "/" + id + "/" + string(action)
	return p.err
}

func decisionsServer(t *testing.T, backend *decisionBackend) (*config.Config, *server.Server) {
	t.Helper()
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterCompleter("chat", backend)
	cfg.RegisterDecider("chat", backend)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, s
}

func TestDecisionsRoutes(t *testing.T) {
	backend := &decisionBackend{}
	_, s := decisionsServer(t, backend)
	body := `{"model":"chat","state":{"ticket":"hello","id":9007199254740993},"questions":{
		"choice":{"type":"choice","instructions":{"ask":"Which team?"},"criteria":{"billing":null,"sales":["Plans"]}},
		"noul":{"type":"noul","instructions":"Is it urgent?","criteria":{"true":{"meaning":"Act now"},"false":null}},
		"score":{"type":"score","instructions":["Rate severity"],"criteria":[null,{"meaning":"High","id":9007199254740993}]}
	}}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Model   string                               `json:"model"`
		Answers map[string]map[string]jsontext.Value `json:"answers"`
		Usage   map[string]int                       `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Model != "actual-model" || result.Usage["input_tokens"] != 50 || result.Usage["output_tokens"] != 20 || backend.calls != 1 {
		t.Fatalf("response: %s, calls %d", w.Body.String(), backend.calls)
	}
	if string(result.Answers["noul"]["noul"]) != "0" || len(result.Answers["noul"]) != 2 {
		t.Fatalf("noul must preserve zero and omit other variant fields: %s", w.Body.String())
	}
	if string(result.Answers["score"]["score"]) != "0" || string(result.Answers["choice"]["confidence"]) != "0" {
		t.Fatalf("missing zero values: %s", w.Body.String())
	}
	state, _ := json.Marshal(backend.input.State)
	if !strings.Contains(string(result.Answers["score"]["legend"]), `"id":9007199254740993`) || !strings.Contains(string(state), `"id":9007199254740993`) {
		t.Fatalf("large integers lost: %s; state %s", w.Body.String(), state)
	}
}

func TestDecisionsStateForms(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{`"hello"`, `"hello"`}, {`["hello",9007199254740993]`, `["hello",9007199254740993]`},
		{`{"text":"hello"}`, `{"text":"hello"}`}, {`null`, `null`},
	} {
		t.Run(tc.state, func(t *testing.T) {
			backend := &decisionBackend{}
			_, s := decisionsServer(t, backend)
			body := `{"model":"chat","state":` + tc.state + `,"questions":{"q":{"type":"noul"}}}`
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if state, _ := json.Marshal(backend.input.State); string(state) != tc.want {
				t.Fatalf("state = %s, want %s", state, tc.want)
			}
		})
	}
}

func TestDecisionsValidation(t *testing.T) {
	for name, body := range map[string]string{
		"invalid json": `{`, "null request": `null`,
		"unknown model":             `{"model":"missing","state":{},"questions":{"q":{"type":"noul"}}}`,
		"missing state":             `{"model":"chat","questions":{"q":{"type":"noul"}}}`,
		"scalar state":              `{"model":"chat","state":true,"questions":{"q":{"type":"noul"}}}`,
		"empty questions":           `{"model":"chat","state":{},"questions":{}}`,
		"null question":             `{"model":"chat","state":{},"questions":{"q":null}}`,
		"unknown type":              `{"model":"chat","state":{},"questions":{"q":{"type":"boolean"}}}`,
		"invalid instructions":      `{"model":"chat","state":{},"questions":{"q":{"type":"noul","instructions":123}}}`,
		"noul criteria":             `{"model":"chat","state":{},"questions":{"q":{"type":"noul","criteria":{"yes":"yes"}}}}`,
		"choice criteria":           `{"model":"chat","state":{},"questions":{"q":{"type":"choice","criteria":[]}}}`,
		"choice scalar description": `{"model":"chat","state":{},"questions":{"q":{"type":"choice","criteria":{"a":1}}}}`,
		"score object":              `{"model":"chat","state":{},"questions":{"q":{"type":"score","criteria":{"0":"a","1":"b"}}}}`,
		"box without image":         `{"model":"chat","state":"no image","questions":{"q":{"type":"box","instructions":"find cat"}}}`,
		"box with criteria":         `{"model":"chat","state":{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}},"questions":{"q":{"type":"box","instructions":"find cat","criteria":{"a":null}}}}`,
		"trailing json":             `{"model":"chat","state":{},"questions":{"q":{"type":"noul"}}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			backend := &decisionBackend{}
			_, s := decisionsServer(t, backend)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
			if w.Code != http.StatusBadRequest || backend.calls != 0 || !jsontext.Value(w.Body.Bytes()).IsValid() {
				t.Fatalf("status %d: %s, calls %d", w.Code, w.Body.String(), backend.calls)
			}
		})
	}
}

func TestDecisionsMultimodalAndBox(t *testing.T) {
	backend := &decisionBackend{noul: 0.8}
	_, s := decisionsServer(t, backend)
	body := `{"model":"chat","state":["photo","caption"],"assets":{"photo":{"type":"image","format":"png","data_base64":"aGk="}},"questions":{
		"box":{"type":"box","instructions":"Locate the object"},
		"noul":{"type":"noul","instructions":["photo","Does it match?"]}
	}}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Answers map[string]struct {
			Type       string                   `json:"type"`
			Noul       float64                  `json:"noul"`
			Box        *provider.BoxCoordinates `json:"box"`
			Confidence *float64                 `json:"confidence"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Answers["box"].Box == nil || result.Answers["box"].Box.Right != 0.68 || result.Answers["box"].Confidence != nil || result.Answers["noul"].Noul != 0.8 || !strings.Contains(w.Body.String(), `"confidence":null`) {
		t.Fatalf("unexpected result: %s", w.Body.String())
	}
	if backend.input.Assets["photo"] == nil {
		t.Fatalf("image asset not attached: %+v", backend.input)
	}
}

func TestDecisionsAccessAndErrors(t *testing.T) {
	const body = `{"model":"chat","state":{},"questions":{"q":{"type":"noul"}}}`
	for _, tc := range []struct {
		name string
		code int
		err  error
	}{
		{"policy", http.StatusNotFound, nil},
		{"auth", http.StatusUnauthorized, nil},
		{"rate limit", http.StatusTooManyRequests, &provider.ProviderError{Code: 429, Type: "rate_limit", Message: "slow down", RetryAfter: 2 * time.Second}},
		{"overloaded", 529, &provider.ProviderError{Code: 529, Message: "overloaded"}},
		{"backend failure", http.StatusBadGateway, errors.New("broken")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &decisionBackend{err: tc.err}
			cfg, s := decisionsServer(t, backend)
			p := &decisionPolicy{}
			cfg.Policy = p
			if tc.name == "policy" {
				p.err = policy.ErrAccessDenied
			}
			if tc.name == "auth" {
				authorizer, err := static.New("test-token")
				if err != nil {
					t.Fatal(err)
				}
				cfg.Authorizers = []auth.Provider{authorizer}
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body)))
			if w.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if tc.name == "auth" || tc.name == "policy" {
				if backend.calls != 0 {
					t.Fatal("denied request reached the model")
				}
			}
			if tc.name != "auth" && p.seen != "model/chat/access" {
				t.Fatalf("policy checked %q", p.seen)
			}
			if tc.name == "rate limit" && w.Header().Get("Retry-After") != "2" {
				t.Fatalf("retry header = %q", w.Header().Get("Retry-After"))
			}
		})
	}
}
