package typesafe

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider"
)

func choiceInput() *provider.DecisionInput {
	return &provider.DecisionInput{
		State: "Our checkout has returned 500 errors since 9am.",
		Questions: []provider.DecisionQuestion{{
			ID: "label", Instructions: "Which label fits this ticket?",
			Choice: &provider.ChoiceQuestion{Options: []provider.DecisionOption{{Label: "billing"}, {Label: "bug"}, {Label: "account"}}},
		}},
	}
}

const choiceResponse = `{"model":"nimble","answers":{"label":{"type":"choice","choice":"bug","probabilities":{"billing":0.011,"bug":0.979,"account":0.010},"confidence":0.892}},"usage":{"input_tokens":168,"output_tokens":1}}`

func TestDeciderPreservesStateAndAnswers(t *testing.T) {
	for _, state := range []string{`"hello"`, `["hello",9007199254740993]`, `{"text":"hello","id":9007199254740993}`, `null`} {
		t.Run(state, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected upstream request: %s %s %v", r.Method, r.URL.Path, r.Header)
				}
				var body struct {
					Model     string         `json:"model"`
					State     jsontext.Value `json:"state"`
					Questions map[string]struct {
						Type         string         `json:"type"`
						Instructions string         `json:"instructions"`
						Criteria     map[string]any `json:"criteria"`
					} `json:"questions"`
				}
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
				}
				q := body.Questions["label"]
				if body.Model != "nimble" || string(body.State) != state || q.Type != "choice" || q.Instructions != "Which label fits this ticket?" || len(q.Criteria) != 3 || q.Criteria["bug"] != nil {
					t.Errorf("request lost state or questions: %+v", body)
				}
				io.WriteString(w, choiceResponse)
			}))
			defer upstream.Close()
			d, err := NewDecider(upstream.URL+"/v1/systemone", "nimble", WithToken("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			input := choiceInput()
			input.State = jsontext.Value(state)
			result, err := d.Decide(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			answer := result.Answers[0].Choice
			if result.Model != "nimble" || result.Usage.InputTokens != 168 || result.Usage.OutputTokens != 1 ||
				answer.Choice != "bug" || answer.Confidence != 0.892 || answer.Probabilities["bug"] != 0.979 {
				t.Fatalf("upstream answer changed: %+v, %+v", result, answer)
			}
		})
	}
}

func TestDeciderRejectsMalformedAnswers(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `not JSON`,
		`{"model":"m","answers":{}}`,
		`{"model":"m","answers":{"other":{"type":"choice"}}}`,
		`{"model":"m","answers":{"label":{"type":"noul","noul":0.9}}}`,
		`{"model":"m","answers":{"label":{"type":"choice","choice":"bug","confidence":0.8,"probabilities":{"billing":0,"bug":1}}}}`,
		`{"model":"m","answers":{"label":{"type":"choice","choice":"missing","confidence":0.8,"probabilities":{"billing":0,"bug":1,"account":0}}}}`,
		`{"model":"m","answers":{"label":{"type":"choice","choice":"bug","confidence":2,"probabilities":{"billing":0,"bug":1,"account":0}}}}`,
		`{"model":"m","answers":{"label":{"type":"choice","choice":"bug","confidence":0.8,"probabilities":{"billing":-1,"bug":1,"account":0}}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, body)
			}))
			defer upstream.Close()
			d, _ := NewDecider(upstream.URL, "nimble", WithMaxRetries(0))
			if _, err := d.Decide(context.Background(), choiceInput()); err == nil {
				t.Fatal("malformed upstream answer was accepted")
			}
		})
	}
}

func TestDeciderUpstreamErrors(t *testing.T) {
	for _, status := range []int{401, 422, 429, 529} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"message":"upstream failed","type":"upstream_error"}}`)
			}))
			defer upstream.Close()
			d, _ := NewDecider(upstream.URL, "nimble", WithMaxRetries(0))
			_, err := d.Decide(context.Background(), choiceInput())
			var p *provider.ProviderError
			if !errors.As(err, &p) || p.Code != status || p.Type != "upstream_error" || p.Message != "upstream failed" || p.RetryAfter != 2*time.Second || calls != 1 {
				t.Fatalf("lost upstream error: %v, calls %d", err, calls)
			}
		})
	}
}

func TestDeciderTypeSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, message, kind string
		status                    int
	}{
		{"string", `{"detail":"Context too long"}`, "Context too long", "", 400},
		{"object", `{"detail":{"message":"Context too long","code":"max_tokens_exceeded","limit":32768}}`, "Context too long", "max_tokens_exceeded", 400},
		{"object type", `{"detail":{"msg":"Slow down","type":"rate_limit_error"}}`, "Slow down", "rate_limit_error", 429},
		{"validation", `{"detail":[{"loc":["body","state"],"msg":"Field required","type":"missing"},{"loc":["body","questions","q","criteria",0],"msg":"Invalid level","type":"value_error"}]}`, "body.state: Field required; body.questions.q.criteria.0: Invalid level", "validation_error", 422},
		{"unknown object", `{"detail":{"reason":"unavailable"}}`, `{"reason":"unavailable"}`, "", 503},
		{"unknown list", `{"detail":[{"unknown":true}]}`, `[{"unknown":true}]`, "validation_error", 422},
		{"null detail", `{"detail":null,"error":{"message":"Denied","type":"auth_error"}}`, "Denied", "auth_error", 401},
		{"plain text", "Service unavailable", "Service unavailable", "", 503},
		{"empty", "", "decision API returned HTTP 500", "", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			d, _ := NewDecider(upstream.URL, "jev-latest", WithMaxRetries(0))
			_, err := d.Decide(context.Background(), choiceInput())
			var upstreamError *provider.ProviderError
			if !errors.As(err, &upstreamError) || upstreamError.Code != tc.status || upstreamError.Message != tc.message || upstreamError.Type != tc.kind || upstreamError.RetryAfter != 2*time.Second {
				t.Fatalf("unexpected error: %+v", upstreamError)
			}
			var envelope struct {
				Detail jsontext.Value `json:"detail"`
			}
			json.Unmarshal([]byte(tc.body), &envelope)
			var detailError *APIError
			wantDetail := len(envelope.Detail) > 0 && string(envelope.Detail) != "null"
			if errors.As(err, &detailError) != wantDetail || wantDetail && string(detailError.Detail) != string(envelope.Detail) {
				t.Fatalf("lost structured detail: %v", err)
			}
		})
	}
}

func TestDeciderRetriesTransientErrors(t *testing.T) {
	for _, status := range []int{401, 422, 429, 529} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.Header().Set("Retry-After", "0.001")
					w.WriteHeader(status)
					io.WriteString(w, `{"error":{"message":"retry"}}`)
					return
				}
				io.WriteString(w, choiceResponse)
			}))
			defer upstream.Close()
			d, _ := NewDecider(upstream.URL, "nimble", WithMaxRetries(1))
			result, err := d.Decide(context.Background(), choiceInput())
			if status == 429 || status == 529 {
				if err != nil || calls != 2 || result.Answers[0].Choice.Choice != "bug" {
					t.Fatalf("transient error not retried: %v, calls %d", err, calls)
				}
			} else if err == nil || calls != 1 {
				t.Fatalf("nontransient error retried: %v, calls %d", err, calls)
			}
		})
	}
}

func TestDeciderCancellationDuringBackoff(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"slow down"}}`)
	}))
	defer upstream.Close()
	d, _ := NewDecider(upstream.URL, "nimble")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := d.Decide(ctx, choiceInput()); !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("backoff ignored cancellation: %v, calls %d", err, calls)
	}
}

func TestDeciderValidatesBeforeCallingUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid input reached upstream")
	}))
	defer upstream.Close()
	d, _ := NewDecider(upstream.URL, "nimble")
	input := choiceInput()
	input.State = true
	if _, err := d.Decide(context.Background(), input); provider.CodeFromError(err, 0) != http.StatusBadRequest {
		t.Fatalf("invalid input error: %v", err)
	}
	if _, err := NewDecider("", strings.Repeat(" ", 2)); err == nil {
		t.Fatal("empty model was accepted")
	}
}
