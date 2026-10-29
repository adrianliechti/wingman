package server

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/budget"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type admissionBudget struct {
	unavailable bool
	checks      atomic.Int32
}

func (b *admissionBudget) Check(context.Context, string) (*budget.Result, error) {
	b.checks.Add(1)
	if b.unavailable {
		return nil, errors.New("offline")
	}
	return &budget.Result{Allowed: false, Message: "limit reached"}, nil
}
func (b *admissionBudget) Report(context.Context, *budget.Report) error {
	return errors.New("denied work must not report")
}

type neverAdmitted struct{ calls atomic.Int32 }

func (m *neverAdmitted) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) { m.calls.Add(1); yield(&provider.Completion{}, nil) }
}

func TestBudgetDenialBeforeHTTPAndSSEHeaders(t *testing.T) {
	authorizer, err := static.New("local-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"missing-user", "exhausted", "unavailable"} {
		for _, stream := range []bool{false, true} {
			for _, api := range []string{"responses", "chat/completions", "messages", "gemini"} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", outcome, api, stream), func(t *testing.T) {
					b := &admissionBudget{unavailable: outcome == "unavailable"}
					m := &neverAdmitted{}
					cfg := &config.Config{Policy: noop.New(), Authorizers: []auth.Provider{authorizer}, Budget: b}
					cfg.RegisterCompleter("test", budget.NewCompleter("test", "upstream", m, b))
					s, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					path := "/v1/" + api
					body := fmt.Sprintf(`{"model":"test","stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`, stream)
					if api == "responses" {
						body = fmt.Sprintf(`{"model":"test","stream":%t,"input":"hello"}`, stream)
					}
					if api == "gemini" {
						path = "/v1beta/models/test:generateContent"
						if stream {
							path = "/v1beta/models/test:streamGenerateContent?alt=sse"
						}
						body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
					}
					req := httptest.NewRequest("POST", path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer local-token")
					want := 401
					if outcome != "missing-user" {
						req.Header.Set("X-Forwarded-User", "user-7")
						want = 429
					}
					if outcome == "unavailable" {
						want = 503
					}
					rec := httptest.NewRecorder()
					s.ServeHTTP(rec, req)
					if rec.Code != want || m.calls.Load() != 0 || strings.Contains(rec.Header().Get("Content-Type"), "event-stream") {
						t.Fatalf("HTTP %d upstream=%d: %s", rec.Code, m.calls.Load(), rec.Body.String())
					}
				})
			}
		}
	}
}
