package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/auth"
)

func upstreamEcho(t *testing.T) (http.RoundTripper, chan string) {
	t.Helper()

	seen := make(chan string, 8)

	s := handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})}

	return s, seen
}

func proxyRequest(t *testing.T, s *Server, token string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), auth.TokenContextKey, token))

	s.ServeHTTP(httptest.NewRecorder(), req)
}

// stubExchanger mirrors obo.Exchanger: it requires a caller token and fails
// without one.
type stubExchanger struct{}

func (stubExchanger) Token(ctx context.Context, assertion string) (string, error) {
	if assertion == "" {
		return "", errors.New("missing assertion")
	}

	return "DOWNSTREAM-" + assertion, nil
}

func TestProxyExchangerReplacesCallerToken(t *testing.T) {
	upstream, seen := upstreamEcho(t)

	s, err := New("https://mcp.test/mcp", nil, stubExchanger{}, WithTransport(upstream))

	if err != nil {
		t.Fatal(err)
	}

	proxyRequest(t, s, "USER-JWT")

	if got := <-seen; got != "Bearer DOWNSTREAM-USER-JWT" {
		t.Errorf("upstream got %q, want the exchanged token", got)
	}
}

// TestProxyRejectsUnauthenticatedCallerWhenExchanging asserts that when an
// exchange is required but the caller is unauthenticated, the request fails
// instead of reaching the upstream with the caller's own credential.
func TestProxyRejectsUnauthenticatedCallerWhenExchanging(t *testing.T) {
	upstream, seen := upstreamEcho(t)

	s, err := New("https://mcp.test/mcp", nil, stubExchanger{}, WithTransport(upstream))

	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer USER-JWT")

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}

	select {
	case got := <-seen:
		t.Errorf("upstream was contacted with %q", got)
	default:
	}
}

// TestProxyPassthroughWithoutCallerDropsInboundToken asserts the caller's own
// credential is not copied through when there is no caller token to forward.
func TestProxyPassthroughWithoutCallerDropsInboundToken(t *testing.T) {
	upstream, seen := upstreamEcho(t)

	s, err := New("https://mcp.test/mcp", nil, auth.PassthroughExchanger{}, WithTransport(upstream))

	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer USER-JWT")

	s.ServeHTTP(httptest.NewRecorder(), req)

	if got := <-seen; got != "" {
		t.Errorf("upstream got %q, want no credential", got)
	}
}

func TestProxyPassthroughSendsCallerToken(t *testing.T) {
	upstream, seen := upstreamEcho(t)

	s, err := New("https://mcp.test/mcp", nil, auth.PassthroughExchanger{}, WithTransport(upstream))

	if err != nil {
		t.Fatal(err)
	}

	proxyRequest(t, s, "USER-JWT")

	if got := <-seen; got != "Bearer USER-JWT" {
		t.Errorf("upstream got %q, want the caller's token", got)
	}
}

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w.Result(), nil
}
