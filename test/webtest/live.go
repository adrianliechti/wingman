package webtest

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/auth"
	"github.com/adrianliechti/wingman/pkg/auth/static"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	exascraper "github.com/adrianliechti/wingman/pkg/scraper/exa"
	"github.com/adrianliechti/wingman/pkg/scraper/fetch"
	tavilyscraper "github.com/adrianliechti/wingman/pkg/scraper/tavily"
	exasearch "github.com/adrianliechti/wingman/pkg/searcher/exa"
	tavilysearch "github.com/adrianliechti/wingman/pkg/searcher/tavily"
	"github.com/adrianliechti/wingman/server"

	"github.com/joho/godotenv"
)

const LiveKey = "wingman-web-e2e"

// LiveServer runs the full ingress with real retrieval providers and a local
// authentication key. Upstream credentials stay server-side and are never logged.
func LiveServer(t *testing.T, first string) *httptest.Server {
	t.Helper()
	if os.Getenv("WEB_LIVE_TEST") != "1" {
		t.Skip("set WEB_LIVE_TEST=1 to run live Tavily/Exa tests")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			_ = godotenv.Load(filepath.Join(dir, ".env"))
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for _, name := range []string{"TAVILY_API_KEY", "EXA_API_KEY"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required for live cross-provider tests", name)
		}
	}
	a, _ := static.New(LiveKey)
	cfg := &config.Config{Policy: noop.New(), Authorizers: []auth.Provider{a}}
	exaSearch, err := exasearch.New(os.Getenv("EXA_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	tavilySearch, err := tavilysearch.New(os.Getenv("TAVILY_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	exaScraper, err := exascraper.New(os.Getenv("EXA_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	tavilyScraper, err := tavilyscraper.New(os.Getenv("TAVILY_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if first == "exa" {
		cfg.RegisterSearcher("exa", otel.NewSearcher("exa", "exa", exaSearch))
		cfg.RegisterScraper("exa", otel.NewScraper("exa", "exa", exaScraper))
	}
	cfg.RegisterSearcher("tavily", otel.NewSearcher("tavily", "tavily", tavilySearch))
	cfg.RegisterScraper("tavily", otel.NewScraper("tavily", "tavily", tavilyScraper))
	cfg.RegisterSearcher("exa", otel.NewSearcher("exa", "exa", exaSearch))
	cfg.RegisterScraper("exa", otel.NewScraper("exa", "exa", exaScraper))
	p, _ := fetch.New()
	cfg.RegisterScraper("fetch", p)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(s)
	local.Client().Timeout = 90 * time.Second
	t.Cleanup(local.Close)
	return local
}
