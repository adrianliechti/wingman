package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/guard/custom"
	"github.com/adrianliechti/wingman/pkg/researcher"

	api "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestWebProviderMiddlewareConfiguration(t *testing.T) {
	guardURL := startFakeGuard(t)
	sentinel := errors.New("upstream called")
	for _, family := range []string{"searchers", "scrapers", "researchers"} {
		for _, guarded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/guarded=%v", family, guarded), func(t *testing.T) {
				recorder := tracetest.NewSpanRecorder()
				provider := sdk.NewTracerProvider(sdk.WithSpanProcessor(recorder))
				previous := api.GetTracerProvider()
				api.SetTracerProvider(provider)
				t.Cleanup(func() {
					api.SetTracerProvider(previous)
					_ = provider.Shutdown(context.Background())
				})
				previousClient := http.DefaultClient
				t.Cleanup(func() { http.DefaultClient = previousClient })
				calls := 0
				http.DefaultClient = &http.Client{Transport: googleTranslatorRoundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return nil, sentinel
				})}
				guardField := ""
				if guarded {
					guardField = ", guard: fake"
				}
				cfg, err := parseTestConfig(t, fmt.Sprintf("guards:\n  fake: {type: custom, url: %s}\n%s:\n  web: {type: exa, token: test%s}\n", guardURL, family, guardField))
				if err != nil {
					t.Fatal(err)
				}
				switch family {
				case "searchers":
					p, e := cfg.Searcher("web")
					if e != nil {
						t.Fatal(e)
					}
					_, err = p.Search(t.Context(), "blocked", nil)
				case "scrapers":
					p, e := cfg.Scraper("web")
					if e != nil {
						t.Fatal(e)
					}
					_, err = p.Scrape(t.Context(), "https://example.com/blocked", nil)
				case "researchers":
					p, e := cfg.Researcher("web")
					if e != nil {
						t.Fatal(e)
					}
					_, err = researcher.Collect(p.Research(t.Context(), "blocked", nil))
				}
				wantSpans, wantCalls := 1, 1
				if guarded {
					wantSpans, wantCalls = 2, 0
					var flagged *guard.FlaggedError
					if !errors.As(err, &flagged) {
						t.Fatalf("error = %v", err)
					}
				} else if !errors.Is(err, sentinel) {
					t.Fatalf("error = %v", err)
				}
				spans := recorder.Ended()
				if len(spans) != wantSpans || calls != wantCalls {
					t.Fatalf("spans=%d calls=%d", len(spans), calls)
				}
				parent := spans[len(spans)-1]
				if parent.Status().Code != codes.Error {
					t.Fatal("provider error was not recorded")
				}
				if guarded {
					child := spans[0]
					if child.Name() != "guard fake" || child.Parent().SpanID() != parent.SpanContext().SpanID() || child.SpanContext().TraceID() != parent.SpanContext().TraceID() {
						t.Fatal("guard span is not a child of provider span")
					}
				}
			})
		}
	}
}
