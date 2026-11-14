package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy"
	"github.com/adrianliechti/wingman/server"
)

func TestDecisionSelectionAndAccess(t *testing.T) {
	for _, endpoint := range []struct{ path, input string }{
		{"/v1/systemone", `"state":"hello","questions":{"q":{"type":"noul"}}`},
		{"/v1/decisions", `"input":"hello","questions":[{"type":"predicate","instructions":""}]`},
	} {
		for _, tt := range []struct {
			name, modelField, wantAlias string
			deciders, denied            bool
			status                      int
		}{
			{name: "default", deciders: true, wantAlias: "z-decider", status: 200},
			{name: "empty model", modelField: `"model":"",`, deciders: true, wantAlias: "z-decider", status: 200},
			{name: "explicit second", modelField: `"model":"a-decider",`, deciders: true, wantAlias: "a-decider", status: 200},
			{name: "completion model", modelField: `"model":"chat",`, deciders: true, status: 400},
			{name: "unknown model", modelField: `"model":"missing",`, deciders: true, status: 400},
			{name: "no decision models", status: 400},
			{name: "denied default", deciders: true, denied: true, wantAlias: "z-decider", status: 404},
		} {
			t.Run(endpoint.path+"/"+tt.name, func(t *testing.T) {
				access := &decisionPolicy{}
				if tt.denied {
					access.err = policy.ErrAccessDenied
				}
				cfg := &config.Config{Policy: access}
				chat := &decisionBackend{}
				cfg.RegisterCompleter("chat", chat)
				first, second := &decisionBackend{}, &decisionBackend{}
				if tt.deciders {
					cfg.RegisterDecider("z-decider", first)
					cfg.RegisterDecider("a-decider", second)
				}
				s, err := server.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				body := "{" + tt.modelField + endpoint.input + "}"
				s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(body)))
				if w.Code != tt.status {
					t.Fatalf("status = %d, want %d: %s", w.Code, tt.status, w.Body.String())
				}
				if tt.wantAlias != "" && access.seen != "model/"+tt.wantAlias+"/access" {
					t.Fatalf("policy checked %q, want alias %q", access.seen, tt.wantAlias)
				}
				wantFirst, wantSecond := 0, 0
				if tt.status == 200 && tt.wantAlias == "z-decider" {
					wantFirst = 1
				}
				if tt.status == 200 && tt.wantAlias == "a-decider" {
					wantSecond = 1
				}
				if first.calls != wantFirst || second.calls != wantSecond || chat.calls != 0 {
					t.Fatalf("calls = %d, %d, chat %d; want %d, %d, 0", first.calls, second.calls, chat.calls, wantFirst, wantSecond)
				}
			})
		}
	}
}
