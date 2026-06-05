package obo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestExchanger(tokenURL string) *Exchanger {
	return &Exchanger{
		client: http.DefaultClient,

		clientID:     "client-id",
		clientSecret: "client-secret",
		scope:        "api://downstream/.default",

		tokenURL: tokenURL,

		cache: make(map[string]entry),
	}
}

func TestExchangeAndCache(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}

		if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", got)
		}

		if got := r.Form.Get("assertion"); got != "user-token" {
			t.Errorf("assertion = %q", got)
		}

		if got := r.Form.Get("scope"); got != "api://downstream/.default" {
			t.Errorf("scope = %q", got)
		}

		if got := r.Form.Get("requested_token_use"); got != "on_behalf_of" {
			t.Errorf("requested_token_use = %q", got)
		}

		if got := r.Form.Get("client_id"); got != "client-id" {
			t.Errorf("client_id = %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"downstream-token","expires_in":3600}`))
	}))

	defer srv.Close()

	e := newTestExchanger(srv.URL)

	token, err := e.Token(context.Background(), "user-token")

	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if token != "downstream-token" {
		t.Fatalf("token = %q", token)
	}

	// second call with the same assertion should hit the cache, no new request
	if _, err := e.Token(context.Background(), "user-token"); err != nil {
		t.Fatalf("Token (cached): %v", err)
	}

	if n := calls.Load(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cache miss)", n)
	}
}

func TestExchangeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))

	defer srv.Close()

	e := newTestExchanger(srv.URL)

	if _, err := e.Token(context.Background(), "user-token"); err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}

func TestStoreEvictsExpired(t *testing.T) {
	e := newTestExchanger("http://unused.invalid")

	e.store("expired", "token-1", time.Now().Add(-time.Minute))
	e.store("live", "token-2", time.Now().Add(time.Hour))

	if _, ok := e.cache["expired"]; ok {
		t.Error("expired entry should not be stored")
	}

	e.cache["stale"] = entry{token: "token-3", expires: time.Now().Add(-time.Minute)}

	e.store("live-2", "token-4", time.Now().Add(time.Hour))

	if _, ok := e.cache["stale"]; ok {
		t.Error("stale entry should be evicted on store")
	}

	if len(e.cache) != 2 {
		t.Errorf("cache size = %d, want 2", len(e.cache))
	}
}

func TestTokenRequiresAssertion(t *testing.T) {
	e := newTestExchanger("http://unused.invalid")

	if _, err := e.Token(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty assertion")
	}
}
