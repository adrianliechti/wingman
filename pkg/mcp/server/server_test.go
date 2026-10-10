package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeProvider struct {
	mu    sync.Mutex
	names []string
	err   error
}

func (p *fakeProvider) set(names []string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.names = names
	p.err = err
}

func (p *fakeProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.err != nil {
		return nil, p.err
	}

	var result []tool.Tool

	for _, name := range p.names {
		result = append(result, tool.Tool{
			Name:        name,
			Description: "echoes its input",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": map[string]any{"type": "string"}},
			},
		})
	}

	return result, nil
}

func (p *fakeProvider) Execute(ctx context.Context, name string, parameters map[string]any) (any, error) {
	text, _ := parameters["text"].(string)
	return text, nil
}

var _ tool.Provider = (*fakeProvider)(nil)

func connectTo(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   "https://mcp.test/mcp",
		HTTPClient: &http.Client{Transport: handlerTransport{s}},
	}, nil)

	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() { session.Close() })

	return session
}

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w.Result(), nil
}

func newServer(t *testing.T, instructions string, providers ...tool.Provider) *Server {
	t.Helper()

	s, err := New("wingman-test", instructions, providers)

	if err != nil {
		t.Fatal(err)
	}

	if err := s.refreshTools(); err != nil {
		t.Fatal(err)
	}

	return s
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	result, err := session.ListTools(t.Context(), nil)

	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for _, tl := range result.Tools {
		names = append(names, tl.Name)
	}

	slices.Sort(names)

	return names
}

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()

	return connectTo(t, newServer(t, "", &fakeProvider{names: []string{"echo"}}))
}

// TestNegotiatesLatestProtocol asserts our streamable server answers
// server/discover (SEP-2575) so clients land on the stateless 2026-07-28
// lifecycle rather than falling back to the legacy initialize handshake.
func TestNegotiatesLatestProtocol(t *testing.T) {
	session := connect(t)

	result := session.InitializeResult()

	if result == nil {
		t.Fatal("no initialize result")
	}

	if result.ProtocolVersion != "2026-07-28" {
		t.Errorf("protocol version = %q, want 2026-07-28", result.ProtocolVersion)
	}
}

// TestServerInfo asserts the server identifies itself with both name and
// version; version is required by the spec's Implementation schema.
func TestServerInfo(t *testing.T) {
	session := connect(t)

	info := session.InitializeResult().ServerInfo

	if info == nil {
		t.Fatal("no server info")
	}

	if info.Name != "wingman-test" {
		t.Errorf("name = %q", info.Name)
	}

	if info.Version == "" {
		t.Error("version is empty, but required by the spec")
	}
}

func TestInstructions(t *testing.T) {
	session := connectTo(t, newServer(t, "be concise"))

	if got := session.InitializeResult().Instructions; got != "be concise" {
		t.Errorf("instructions = %q, want %q", got, "be concise")
	}
}

// TestRefreshRetiresRemovedTools asserts a tool that disappears upstream stops
// being advertised, rather than lingering from an earlier refresh.
func TestRefreshRetiresRemovedTools(t *testing.T) {
	p := &fakeProvider{names: []string{"alpha", "beta"}}

	s := newServer(t, "", p)

	p.set([]string{"alpha"}, nil)

	if err := s.refreshTools(); err != nil {
		t.Fatal(err)
	}

	if got := toolNames(t, connectTo(t, s)); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("tools = %v, want [alpha]", got)
	}
}

// TestRefreshKeepsToolsOnUpstreamError asserts a transient provider failure
// does not retract tools that are still presumed live.
func TestRefreshKeepsToolsOnUpstreamError(t *testing.T) {
	p := &fakeProvider{names: []string{"alpha"}}

	s := newServer(t, "", p)

	p.set(nil, errors.New("upstream down"))

	if err := s.refreshTools(); err == nil {
		t.Fatal("expected error")
	}

	if got := toolNames(t, connectTo(t, s)); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("tools = %v, want [alpha] retained", got)
	}
}

// TestRefreshIsQuietWhenNothingChanged asserts an unchanged refresh does not
// wake connected clients: AddTool always reports a change, so re-adding every
// tool on each cycle would emit a tools/list_changed every 5 minutes.
func TestRefreshIsQuietWhenNothingChanged(t *testing.T) {
	p := &fakeProvider{names: []string{"alpha"}}

	s := newServer(t, "", p)

	notified := make(chan struct{}, 8)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			notified <- struct{}{}
		},
	})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := client.Connect(t.Context(), clientTransport, nil)

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { session.Close() })

	if err := s.refreshTools(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-notified:
		t.Error("unchanged refresh emitted tools/list_changed")
	case <-time.After(time.Second):
	}

	p.set([]string{"alpha", "beta"}, nil)

	if err := s.refreshTools(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Error("real change did not emit tools/list_changed")
	}
}

func TestListAndCallTool(t *testing.T) {
	ctx := t.Context()
	session := connect(t)

	if got := toolNames(t, session); !slices.Equal(got, []string{"echo"}) {
		t.Fatalf("tools = %v", got)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})

	if err != nil {
		t.Fatal(err)
	}

	if result.IsError {
		t.Fatalf("tool returned error: %+v", result.Content)
	}

	text, ok := result.Content[0].(*mcp.TextContent)

	if !ok || text.Text != "hello" {
		t.Errorf("content = %+v", result.Content)
	}
}
