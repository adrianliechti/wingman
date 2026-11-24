package harness

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireConfigured(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status               int
		body                 string
		unavailable, missing bool
	}{
		{"configured OpenAI model", http.StatusOK, `{"data":[{"id":"claude-haiku-5-5"}]}`, false, false},
		{"configured Gemini model", http.StatusOK, `{"models":[{"name":"models/claude-haiku-5-5"}]}`, false, false},
		{"model missing", http.StatusOK, `{"data":[{"id":"other-model"}]}`, false, true},
		{"empty listing", http.StatusOK, `{"data":[]}`, true, false},
		{"invalid listing", http.StatusOK, `not json`, true, false},
		{"unauthorized", http.StatusUnauthorized, `{"error":"unauthorized"}`, true, false},
		{"unavailable", 0, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("unexpected preflight request: %s, authorization %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			if tc.status == 0 {
				server.Close()
			}
			for range 2 {
				unavailable, missing := checkConfigured(server.URL+"/v1", "test-key", "claude-haiku-5-5")
				if unavailable != tc.unavailable || missing != tc.missing {
					t.Fatalf("unavailable, missing = %v, %v; want %v, %v", unavailable, missing, tc.unavailable, tc.missing)
				}
			}
			if tc.status != 0 && requests != 1 {
				t.Fatalf("model listing requested %d times, want one cached preflight", requests)
			}
		})
	}
}
