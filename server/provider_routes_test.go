package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/auth/static"
)

func TestProviderRoutes(t *testing.T) {
	cfg, s := decisionsServer(t, &decisionBackend{output: "hello", noul: 1})
	key, err := static.New("sdk-key")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Authorizers = append(cfg.Authorizers, key)

	const googleMessage = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	const openaiDecision = `{"model":"chat","input":"hi","questions":[{"name":"q","type":"predicate","instructions":"Is it a greeting?"}]}`
	const messages = `{"model":"chat","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`

	type endpoint struct{ path, body, want string }
	for _, api := range []struct {
		roots       []string
		header, key string
		methods     []endpoint
	}{
		{[]string{"/v1beta", "/v1"}, "x-goog-api-key", "sdk-key", []endpoint{
			{"/models/chat:generateContent", googleMessage, `"text":"hello"`},
			{"/models/chat:streamGenerateContent?alt=sse", googleMessage, `"finishReason":"STOP"`},
			{"/models/chat:countTokens", googleMessage, `"totalTokens"`},
		}},
		{[]string{"/v1"}, "Authorization", "Bearer sdk-key", []endpoint{
			{"/models", "", `"object":"list"`},
			{"/models/chat", "", `"object":"model"`},
			{"/responses", `{"model":"chat","input":"hi"}`, `"object":"response"`},
			{"/decisions", openaiDecision, `"input_tokens"`},
		}},
		{[]string{"/v1"}, "x-api-key", "sdk-key", []endpoint{
			{"/messages", messages, `"type":"message"`},
			{"/messages/count_tokens", messages, `"input_tokens"`},
		}},
	} {
		for _, root := range api.roots {
			for _, endpoint := range api.methods {
				t.Run(root+endpoint.path, func(t *testing.T) {
					method := http.MethodPost
					if endpoint.body == "" {
						method = http.MethodGet
					}
					r := httptest.NewRequest(method, root+endpoint.path, strings.NewReader(endpoint.body))
					r.Header.Set(api.header, api.key)
					w := httptest.NewRecorder()
					s.ServeHTTP(w, r)
					if w.Code != 200 || !strings.Contains(w.Body.String(), endpoint.want) {
						t.Errorf("HTTP %d: %s, want %s", w.Code, w.Body.String(), endpoint.want)
					}
				})
			}
		}
	}

	for _, path := range []string{"/v1beta/decisions", "/v1beta/responses", "/v1beta/models"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.Header.Set("Authorization", "Bearer sdk-key")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("unexpected route %s: HTTP %d", path, w.Code)
		}
	}
	for _, tc := range []struct{ path, header, bearer, want string }{
		{"/v1beta/models/chat:generateContent", "x-goog-api-key", "invalid", `"UNAUTHENTICATED"`},
		{"/v1/models/chat:countTokens", "x-goog-api-key", "invalid", `"UNAUTHENTICATED"`},
		{"/v1/messages", "x-api-key", "invalid", `"authentication_error"`},
		{"/v1/messages", "x-goog-api-key", "", `"authentication_error"`},
		{"/v1/decisions", "x-goog-api-key", "", `"invalid_api_key"`},
		{"/v1/responses", "x-api-key", "", `"invalid_api_key"`},
		{"GET /v1/models/chat", "x-goog-api-key", "", `"invalid_api_key"`},
	} {
		method, path := http.MethodPost, tc.path
		if before, after, ok := strings.Cut(tc.path, " "); ok {
			method, path = before, after
		}
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set(tc.header, "sdk-key")
		if tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s accepted credentials from %s: HTTP %d: %s", tc.path, tc.header, w.Code, w.Body.String())
		}
	}
}
