package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// authEcho stands up an MCP server that records the Authorization header of
// every request it receives.
func authEcho(t *testing.T) (http.Handler, chan string) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "noop", Description: "d"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	seen := make(chan string, 8)

	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		handler.ServeHTTP(w, r)
	})
	return echo, seen
}

func callerCtx(t *testing.T, token string) context.Context {
	t.Helper()

	return context.WithValue(t.Context(), auth.TokenContextKey, token)
}

// TestPassthroughSendsCallerToken asserts the caller's JWT reaches the
// upstream MCP server when passthrough auth is configured.
func TestPassthroughSendsCallerToken(t *testing.T) {
	url, seen := authEcho(t)

	c, err := newHTTPTestClient(t, url, nil, auth.PassthroughExchanger{})

	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tools(callerCtx(t, "USER-JWT")); err != nil {
		t.Fatal(err)
	}

	if got := <-seen; got != "Bearer USER-JWT" {
		t.Errorf("upstream got %q, want the caller's token", got)
	}
}

// TestNoExchangerWithholdsCallerToken asserts the caller's JWT is not sent to
// an upstream that was not configured to receive it.
func TestNoExchangerWithholdsCallerToken(t *testing.T) {
	url, seen := authEcho(t)

	c, err := newHTTPTestClient(t, url, nil, nil)

	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tools(callerCtx(t, "USER-JWT")); err != nil {
		t.Fatal(err)
	}

	if got := <-seen; got != "" {
		t.Errorf("upstream got %q, want no credential", got)
	}
}

// TestStaticSendsFixedToken asserts static auth sends its configured
// credential even when the caller is unauthenticated.
func TestStaticSendsFixedToken(t *testing.T) {
	url, seen := authEcho(t)

	c, err := newHTTPTestClient(t, url, nil, auth.NewStaticExchanger("SERVICE-TOKEN"))

	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := <-seen; got != "Bearer SERVICE-TOKEN" {
		t.Errorf("upstream got %q, want the configured token", got)
	}
}

// TestPassthroughWithoutCallerSendsNothing asserts an unauthenticated caller
// results in no credential rather than an empty bearer header.
func TestPassthroughWithoutCallerSendsNothing(t *testing.T) {
	url, seen := authEcho(t)

	c, err := newHTTPTestClient(t, url, nil, auth.PassthroughExchanger{})

	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := <-seen; got != "" {
		t.Errorf("upstream got %q, want no credential", got)
	}
}

type stubExchanger struct{}

func (stubExchanger) Token(ctx context.Context, assertion string) (string, error) {
	return "DOWNSTREAM-" + assertion, nil
}

// TestExchangerReplacesCallerToken asserts an on-behalf-of style exchange sends
// the downstream token rather than the caller's own.
func TestExchangerReplacesCallerToken(t *testing.T) {
	url, seen := authEcho(t)

	c, err := newHTTPTestClient(t, url, nil, stubExchanger{})

	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Tools(callerCtx(t, "USER-JWT")); err != nil {
		t.Fatal(err)
	}

	if got := <-seen; got != "Bearer DOWNSTREAM-USER-JWT" {
		t.Errorf("upstream got %q, want the exchanged token", got)
	}
}
