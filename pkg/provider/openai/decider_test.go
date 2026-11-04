package openai

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
	"github.com/adrianliechti/wingman/pkg/provider"
)

func TestDeciderNativeQuestions(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/custom/v1/decisions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Model     string         `json:"model"`
			Input     jsontext.Value `json:"input"`
			Questions []struct {
				Type         string `json:"type"`
				Instructions string `json:"instructions"`
				Choices      []struct {
					Value any `json:"value"`
				} `json:"choices"`
				Levels []struct {
					Label string `json:"label"`
				} `json:"levels"`
			} `json:"questions"`
		}
		if err := json.UnmarshalRead(r.Body, &req); err != nil {
			t.Error(err)
		}
		if req.Model != "native-model" || string(req.Input) != `"{\"id\":9007199254740993,\"ticket\":\"broken\"}"` ||
			req.Questions[0].Type != "predicate" || !strings.Contains(req.Questions[0].Instructions, "Criteria") ||
			req.Questions[1].Choices[0].Value != false || req.Questions[1].Choices[1].Value != "false" || req.Questions[2].Levels[1].Label != "high" {
			t.Errorf("lost typed input: %+v", req)
		}
		io.WriteString(w, `{"model":"actual-model","answers":[
   {"type":"predicate","name":"p","probability":0},
   {"type":"choice","name":"c","choice":false,"confidence":0,"probabilities":[{"value":false,"probability":1},{"value":"false","probability":0}]},
   {"type":"score","name":"s","score":0,"confidence":0,"probabilities":[{"value":1,"label":"high","probability":0},{"value":0,"label":"low","probability":1}]},
   {"type":"refusal","name":"r"}
  ],"usage":{"input_tokens":42,"output_tokens":3,"input_tokens_details":{"cached_tokens":12,"cache_write_tokens":4},"output_tokens_details":{"reasoning_tokens":2},"total_tokens":45}}`)
	}))
	defer upstream.Close()
	d, err := NewDecider(upstream.URL+"/custom/v1", "native-model", WithToken("test-key"), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	input := &provider.DecisionInput{State: map[string]any{"ticket": "broken", "id": jsonutil.Number("9007199254740993")}, Questions: []provider.DecisionQuestion{
		{ID: "p", Instructions: "Damaged?", Noul: &provider.NoulQuestion{True: "Broken"}},
		{ID: "c", Instructions: "Choose", Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "bool", Value: false}, {Label: "text", Value: "false"}}}},
		{ID: "s", Instructions: "Rate", Score: &provider.ScoreQuestion{Levels: []any{"Low", "High"}, Labels: []string{"low", "high"}}},
		{ID: "r", Noul: &provider.NoulQuestion{}},
	}}
	result, err := d.Decide(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Model != "actual-model" || result.Answers[0].Noul.Probability != 0 ||
		result.Answers[1].Choice.Choice != "bool" ||
		result.Answers[2].Score.Score != 0 || result.Answers[2].Score.Levels[0].Probability != 1 ||
		result.Answers[3].Refusal == nil || result.Usage.CacheReadInputTokens != 12 || result.Usage.CacheCreationInputTokens != 4 ||
		result.Usage.ReasoningTokens == nil || *result.Usage.ReasoningTokens != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := d.Decide(context.Background(), nil); provider.CodeFromError(err, 0) != http.StatusBadRequest || calls != 1 {
		t.Fatalf("invalid input reached upstream: %v", err)
	}
}

func TestDeciderKeepsApplicationStateAsText(t *testing.T) {
	d, _ := NewDecider("", "gpt-6-luna")
	for _, state := range []string{`[{"role":"system","uptime":5}]`, `[{"role":"user","content":"hello","uptime":5}]`, `[{"type":"function_call","uptime":5}]`} {
		req, err := d.convertDecisionRequest(&provider.DecisionInput{State: jsontext.Value(state), Questions: []provider.DecisionQuestion{{ID: "q", Instructions: jsontext.Value(`["uptime"]`), Noul: &provider.NoulQuestion{True: "five"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if req.Input.OfString.Value != state || !strings.Contains(req.Questions[0].OfPredicate.Instructions.OfString.Value, "Criteria") {
			t.Fatalf("state or criteria changed: %+v", req)
		}
	}
}

func TestDeciderErrorsAndRetries(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		w.Header().Set("Retry-After", "0.001")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"type":"rate_limit_exceeded","code":"rate_limit_exceeded","message":"busy"}}`)
	}))
	defer upstream.Close()
	input := &provider.DecisionInput{State: "hello", Questions: []provider.DecisionQuestion{{ID: "p", Noul: &provider.NoulQuestion{}}}}
	for _, retries := range []int{0, 1} {
		d, _ := NewDecider(upstream.URL+"/v1", "model", WithMaxRetries(retries))
		calls = 0
		_, err := d.Decide(context.Background(), input)
		if calls != retries+1 || provider.CodeFromError(err, 0) != 429 || provider.RetryAfterFromError(err) != time.Millisecond {
			t.Fatalf("calls %d, error %v", calls, err)
		}
	}
	d, _ := NewDecider(upstream.URL+"/v1", "model")
	calls = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Decide(ctx, input); err == nil || calls != 0 {
		t.Fatalf("cancelled request reached upstream: %v", err)
	}
}
