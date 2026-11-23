package keenable

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestSearchKeyless(t *testing.T) {
	limit := 2
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	options := &searcher.SearchOptions{Limit: &limit, Exclude: []string{"spam.example"}, Since: &since, Until: &until, Location: "CH"}
	want := *options

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://api.keenable.ai/v1/search/public" {
			t.Fatalf("wrong request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Keenable-Title") != "wingman" || r.Header.Get("X-API-Key") != "" {
			t.Fatalf("wrong headers: %v", r.Header)
		}
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		wantBody := map[string]any{
			"query":            "go release",
			"max_results":      float64(2),
			"exclude_domains":  []any{"spam.example"},
			"published_after":  "2026-10-01T00:00:00Z",
			"published_before": "2026-11-01T00:00:00Z",
		}
		if !reflect.DeepEqual(body, wantBody) {
			t.Fatalf("body = %v", body)
		}
		return respond(http.StatusOK, `{"query":"go release","results":[
			{"title":"Go 1.27","url":"https://go.dev/doc/go1.27","description":"","snippet":"Release notes.","published_at":"2026-10-20T03:44:34Z"},
			{"title":"No snippet","url":"https://example.org/b","description":"Fallback description."},
			{"title":"No URL","url":""}
		]}`), nil
	})}

	c, _ := New("", WithClient(httpClient))
	results, err := c.Search(context.Background(), " go release ", options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*options, want) {
		t.Fatal("mutated caller options")
	}
	if len(results) != 2 {
		t.Fatalf("got %d results: %+v", len(results), results)
	}
	if results[0].Source != "https://go.dev/doc/go1.27" || results[0].Title != "Go 1.27" || results[0].Excerpt != "Release notes." {
		t.Fatalf("wrong first result: %+v", results[0])
	}
	if results[0].Timestamp == nil || results[0].Metadata["published"] != "2026-10-20T03:44:34Z" {
		t.Fatalf("missing timestamp: %+v", results[0])
	}
	if results[1].Excerpt != "Fallback description." || results[1].Timestamp != nil {
		t.Fatalf("wrong second result: %+v", results[1])
	}
}

func TestSearchKeyedAndIncludedDomains(t *testing.T) {
	var sites []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.keenable.ai/v1/search" || r.Header.Get("X-API-Key") != "secret" || r.Header.Get("X-Keenable-Title") != "" {
			t.Fatalf("wrong request: %s %v", r.URL, r.Header)
		}
		var body map[string]any
		_ = json.UnmarshalRead(r.Body, &body)
		site, _ := body["site"].(string)
		sites = append(sites, site)
		if site == "example.com" {
			return respond(http.StatusOK, `{"results":[{"title":"A","url":"https://example.com/a"},{"title":"Dup","url":"https://example.org/b"}]}`), nil
		}
		return respond(http.StatusOK, `{"results":[{"title":"Dup","url":"https://example.org/b"},{"title":"C","url":"https://example.org/c"}]}`), nil
	})}

	limit := 2
	c, _ := New(" secret ", WithClient(httpClient))
	results, err := c.Search(context.Background(), "q", &searcher.SearchOptions{Limit: &limit, Include: []string{"example.com", "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sites, []string{"example.com", "example.org"}) {
		t.Fatalf("sites = %v", sites)
	}
	if len(results) != 2 || results[0].Source != "https://example.com/a" || results[1].Source != "https://example.org/b" {
		t.Fatalf("wrong results: %+v", results)
	}
}

func TestSearchErrors(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return respond(http.StatusTooManyRequests, `{"error":"rate limited"}`), nil
	})}
	c, _ := New("", WithClient(httpClient), WithTitle("custom-app"))

	if _, err := c.Search(context.Background(), "q", nil); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Search(context.Background(), "  ", nil); err == nil {
		t.Fatal("expected empty query error")
	}
	limit := 51
	if _, err := c.Search(context.Background(), "q", &searcher.SearchOptions{Limit: &limit}); err == nil {
		t.Fatal("expected limit error")
	}
	now := time.Now()
	if _, err := c.Search(context.Background(), "q", &searcher.SearchOptions{Since: &now, Until: &now}); err == nil {
		t.Fatal("expected date range error")
	}

	missing := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Keenable-Title") != "custom-app" {
			t.Fatalf("title = %q", r.Header.Get("X-Keenable-Title"))
		}
		return respond(http.StatusOK, `{}`), nil
	})}
	c, _ = New("", WithClient(missing), WithTitle("custom-app"))
	if _, err := c.Search(context.Background(), "q", nil); err == nil || !strings.Contains(err.Error(), "missing results") {
		t.Fatalf("err = %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	c, _ := New("")
	if got := c.SearchCapabilities(); !got.DateFilters || got.Content || len(got.Categories) != 0 {
		t.Fatalf("capabilities = %+v", got)
	}
}

// Opt-in real retrieval test against the keyless endpoint.
func TestSearchLive(t *testing.T) {
	if os.Getenv("KEENABLE_LIVE_TEST") != "1" {
		t.Skip("set KEENABLE_LIVE_TEST=1 to exercise keyless Keenable search")
	}
	c, _ := New("")
	limit := 3
	results, err := c.Search(context.Background(), "go programming language release notes", &searcher.SearchOptions{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || !strings.HasPrefix(results[0].Source, "http") {
		t.Fatalf("results = %+v", results)
	}
	t.Logf("%d results, first: %s", len(results), results[0].Source)
}
