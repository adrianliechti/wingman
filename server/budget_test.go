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
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/budget"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router"
	"github.com/adrianliechti/wingman/pkg/router/roundrobin"
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

type userBudget struct {
	offline atomic.Bool
	checks  atomic.Int32
	reports chan *budget.Report
}

func (b *userBudget) Check(_ context.Context, user string) (*budget.Result, error) {
	b.checks.Add(1)
	if b.offline.Load() {
		return nil, errors.New("offline")
	}
	return &budget.Result{Allowed: user != "broke", Message: "limit reached", ResetsAt: time.Now().Add(30 * 24 * time.Hour)}, nil
}
func (b *userBudget) Report(_ context.Context, r *budget.Report) error {
	b.reports <- r
	return nil
}

type answering struct{ calls atomic.Int32 }

func (m *answering) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		m.calls.Add(1)
		yield(&provider.Completion{Model: "upstream", Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("ok")}}, Usage: &provider.Usage{InputTokens: 3, OutputTokens: 1}}, nil)
	}
}

// Budget denials concern one user, never upstream health: a router must keep
// serving everyone else, and a budget outage must not open its circuits.
func TestBudgetDenialsDoNotTripRouterCircuits(t *testing.T) {
	authorizer, err := static.New("local-token")
	if err != nil {
		t.Fatal(err)
	}
	b := &userBudget{reports: make(chan *budget.Report, 16)}
	upstreams := []*answering{{}, {}}
	cfg := &config.Config{Policy: noop.New(), Authorizers: []auth.Provider{authorizer}, Budget: b}
	var leaves []provider.Completer
	for i, m := range upstreams {
		leaves = append(leaves, budget.NewCompleter("test", fmt.Sprintf("upstream-%d", i), m, b))
	}
	routed, err := roundrobin.NewCompleter(leaves, router.WithFailureThreshold(1), router.WithRecoveryTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegisterCompleter("routed", routed)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	send := func(user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"routed","messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer local-token")
		req.Header.Set("X-Forwarded-User", user)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	for range 4 {
		if rec := send("broke"); rec.Code != 429 {
			t.Fatalf("exhausted user got HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	b.offline.Store(true)
	for range 4 {
		if rec := send("rich"); rec.Code != 503 {
			t.Fatalf("budget outage got HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	if upstreams[0].calls.Load()+upstreams[1].calls.Load() != 0 || len(b.reports) != 0 {
		t.Fatal("rejected requests reached upstream or were reported")
	}
	b.offline.Store(false)
	for range 2 {
		if rec := send("rich"); rec.Code != 200 {
			t.Fatalf("router stayed broken for other users: HTTP %d: %s", rec.Code, rec.Body.String())
		}
	}
	// Each request is admitted once, not once per failover candidate.
	if b.checks.Load() != 10 || len(b.reports) != 2 {
		t.Fatalf("checks=%d reports=%d", b.checks.Load(), len(b.reports))
	}
}
