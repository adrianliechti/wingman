package config

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/guard/custom"
	"github.com/adrianliechti/wingman/pkg/researcher"

	"google.golang.org/grpc"
)

type fakeGuardServer struct {
	custom.UnimplementedGuardServer
}

func (s *fakeGuardServer) Check(ctx context.Context, req *custom.CheckRequest) (*custom.CheckResponse, error) {
	if !strings.Contains(strings.ToLower(req.GetText()), "blocked") {
		return &custom.CheckResponse{}, nil
	}

	return &custom.CheckResponse{
		Flagged: true,

		Categories: []*custom.Category{
			{Name: "test", Score: 1},
		},
	}, nil
}

func startFakeGuard(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	custom.RegisterGuardServer(server, &fakeGuardServer{})

	go server.Serve(listener)
	t.Cleanup(server.Stop)

	return "grpc://" + listener.Addr().String()
}

func TestCustomGuardProtectsWebProviders(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("page content"))
	}))
	t.Cleanup(origin.Close)

	cfg, err := parseTestConfig(t, testProviderConfig+`
guards:
  fake:
    type: custom
    url: `+startFakeGuard(t)+`
scrapers:
  web: {type: fetch, guard: fake}
searchers:
  web: {type: duckduckgo, guard: fake}
researchers:
  deep: {type: agent, model: test-model, searcher: web, guard: fake}
`)
	if err != nil {
		t.Fatal(err)
	}

	scraperProvider, err := cfg.Scraper("web")

	if err != nil {
		t.Fatal(err)
	}

	searcherProvider, err := cfg.Searcher("web")

	if err != nil {
		t.Fatal(err)
	}

	researcherProvider, err := cfg.Researcher("deep")

	if err != nil {
		t.Fatal(err)
	}

	if _, err := scraperProvider.Scrape(context.Background(), origin.URL+"/blocked", nil); !isFlagged(err) {
		t.Fatalf("scrape error = %v", err)
	}

	if _, err := searcherProvider.Search(context.Background(), "blocked query", nil); !isFlagged(err) {
		t.Fatalf("search error = %v", err)
	}

	_, err = researcher.Collect(researcherProvider.Research(context.Background(), "blocked instructions", nil))

	if !isFlagged(err) {
		t.Fatalf("research error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("flagged scrape reached the origin")
	}

	document, err := scraperProvider.Scrape(context.Background(), origin.URL+"/article", nil)

	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(document.Text, "page content") {
		t.Fatalf("document = %q", document.Text)
	}
	if requests.Load() != 1 {
		t.Fatalf("origin requests = %d, want 1", requests.Load())
	}
}

func isFlagged(err error) bool {
	var flagged *guard.FlaggedError

	if !errors.As(err, &flagged) {
		return false
	}

	return len(flagged.Categories) == 1 && flagged.Categories[0].Name == "test"
}
