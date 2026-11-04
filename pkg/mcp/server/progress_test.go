package server

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/tool/research"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type researchFunc func(context.Context, string, *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error]

func (f researchFunc) Research(ctx context.Context, instructions string, options *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
	return f(ctx, instructions, options)
}

func researchServer(t *testing.T, f researchFunc) *Server {
	t.Helper()
	p, err := research.New(otel.NewResearcher("agent", "research", f))
	if err != nil {
		t.Fatal(err)
	}
	return newServer(t, "", otel.NewTool("research", p))
}

func progressClient(t *testing.T, s *Server, httpTransport bool) (*mcp.ClientSession, <-chan *mcp.ProgressNotificationParams) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	updates := make(chan *mcp.ProgressNotificationParams, 100)
	client := mcp.NewClient(&mcp.Implementation{Name: "progress-test", Version: "1.0.0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			updates <- req.Params
		},
	})
	var transport mcp.Transport
	if httpTransport {
		// A real listener streams responses and propagates disconnects,
		// unlike a recorder that buffers the whole response.
		server := httptest.NewServer(s)
		t.Cleanup(server.Close)
		transport = &mcp.StreamableClientTransport{Endpoint: server.URL}
	} else {
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		ss, err := s.server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		transport = clientTransport
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, updates
}

func nextProgress(t *testing.T, updates <-chan *mcp.ProgressNotificationParams) *mcp.ProgressNotificationParams {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("missing progress notification")
		return nil
	}
}

func TestResearchProgressHTTP(t *testing.T) {
	for _, token := range []any{"research-1", 0, ""} {
		t.Run(fmt.Sprintf("%T-%v", token, token), func(t *testing.T) {
			s := researchServer(t, func(_ context.Context, instructions string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
				return func(yield func(*researcher.Result, error) bool) {
					if !yield(&researcher.Result{Progress: "Starting research"}, nil) {
						return
					}
					for range 1000 {
						if !yield(&researcher.Result{Progress: "Gathering evidence"}, nil) {
							return
						}
					}
					yield(&researcher.Result{Content: "Report: " + instructions}, nil)
				}
			})
			session, updates := progressClient(t, s, true)
			params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "question"}}
			params.SetProgressToken(token)
			result, err := session.CallTool(t.Context(), params)
			if err != nil || result.IsError || result.Content[0].(*mcp.TextContent).Text != "Report: question" {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			var previous float64
			count := 0
			for {
				u := nextProgress(t, updates)
				if fmt.Sprint(u.ProgressToken) != fmt.Sprint(token) || u.Progress <= previous || u.Total != 0 {
					t.Fatalf("invalid progress = %+v, previous = %v", u, previous)
				}
				previous = u.Progress
				count++
				if u.Message == "Research complete" {
					break
				}
			}
			if count < 2 || count > 10 {
				t.Fatalf("burst generated %d notifications", count)
			}
			select {
			case u := <-updates:
				t.Fatalf("notification after completion: %+v", u)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestResearchWithoutProgressToken(t *testing.T) {
	for _, token := range []any{nil, true, 1.5, map[string]any{"id": "invalid"}} {
		t.Run(fmt.Sprint(token), func(t *testing.T) {
			s := researchServer(t, func(_ context.Context, _ string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
				return func(yield func(*researcher.Result, error) bool) {
					if !yield(&researcher.Result{Progress: "Gathering evidence"}, nil) {
						return
					}
					yield(&researcher.Result{Content: "report"}, nil)
				}
			})
			session, updates := progressClient(t, s, true)
			params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "q"}}
			if token != nil {
				params.SetMeta(map[string]any{"progressToken": token})
			}
			if _, err := session.CallTool(t.Context(), params); err != nil {
				t.Fatal(err)
			}
			select {
			case u := <-updates:
				t.Fatalf("unexpected notification: %+v", u)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestResearchProgressBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	s := researchServer(t, func(ctx context.Context, _ string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
		return func(yield func(*researcher.Result, error) bool) {
			if !yield(&researcher.Result{Progress: "Starting research"}, nil) {
				return
			}
			if !yield(&researcher.Result{Progress: "Reading sources"}, nil) {
				return
			}
			select {
			case <-release:
				yield(&researcher.Result{Content: "report"}, nil)
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
		}
	})
	session, updates := progressClient(t, s, false)
	finished := make(chan error, 1)
	go func() {
		params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "q"}}
		params.SetProgressToken("active")
		_, err := session.CallTool(t.Context(), params)
		finished <- err
	}()
	u := nextProgress(t, updates)
	if u.Message != "Starting research" || u.ProgressToken != "active" {
		t.Fatalf("update = %+v", u)
	}
	if u := nextProgress(t, updates); u.Message != "Reading sources" {
		t.Fatalf("coalesced update = %+v", u)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if u := nextProgress(t, updates); u.Message != "Research complete" {
		t.Fatalf("final update = %+v", u)
	}
}

func TestResearchProgressConcurrentCalls(t *testing.T) {
	s := researchServer(t, func(_ context.Context, instructions string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
		return func(yield func(*researcher.Result, error) bool) {
			for range 100 {
				if !yield(&researcher.Result{Progress: "Evidence for " + instructions}, nil) {
					return
				}
			}
			yield(&researcher.Result{Content: instructions}, nil)
		}
	})
	session, updates := progressClient(t, s, false)
	var wg sync.WaitGroup
	for _, token := range []string{"first", "second"} {
		wg.Go(func() {
			params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": token}}
			params.SetProgressToken(token)
			if _, err := session.CallTool(t.Context(), params); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	previous := map[string]float64{}
	complete := map[string]bool{}
	for len(complete) < 2 {
		u := nextProgress(t, updates)
		token, ok := u.ProgressToken.(string)
		if !ok || (token != "first" && token != "second") || u.Progress <= previous[token] || complete[token] {
			t.Fatalf("invalid concurrent progress: %+v", u)
		}
		previous[token] = u.Progress
		if u.Message == "Research complete" {
			complete[token] = true
		}
	}
}

func TestResearchProgressFailure(t *testing.T) {
	s := researchServer(t, func(context.Context, string, *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
		return func(yield func(*researcher.Result, error) bool) {
			if !yield(&researcher.Result{Progress: "Starting research"}, nil) {
				return
			}
			yield(nil, errors.New("upstream failure"))
		}
	})
	session, updates := progressClient(t, s, true)
	params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "q"}}
	params.SetProgressToken("failure")
	result, err := session.CallTool(t.Context(), params)
	if err == nil && !result.IsError {
		t.Fatal("research failure was lost")
	}
	if u := nextProgress(t, updates); u.Message != "Starting research" {
		t.Fatalf("start = %+v", u)
	}
	if u := nextProgress(t, updates); u.Message != "Research failed" {
		t.Fatalf("failure = %+v", u)
	}
}

func TestResearchProgressCancellation(t *testing.T) {
	for _, httpTransport := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "http"}[httpTransport], func(t *testing.T) {
			testResearchProgressCancellation(t, httpTransport)
		})
	}
}

func testResearchProgressCancellation(t *testing.T, httpTransport bool) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := researchServer(t, func(ctx context.Context, _ string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
		return func(yield func(*researcher.Result, error) bool) {
			defer close(stopped)
			if !yield(&researcher.Result{Progress: "Starting research"}, nil) {
				return
			}
			close(started)
			<-ctx.Done()
			yield(nil, ctx.Err())
		}
	})
	session, updates := progressClient(t, s, httpTransport)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	finished := make(chan error, 1)
	go func() {
		params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "q"}}
		params.SetProgressToken("cancel")
		_, err := session.CallTool(ctx, params)
		finished <- err
	}()
	nextProgress(t, updates)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("research did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not reach researcher")
	}
	select {
	case u := <-updates:
		t.Fatalf("progress after cancellation: %+v", u)
	case <-time.After(2 * progressInterval):
	}
}
