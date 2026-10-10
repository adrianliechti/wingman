package duckduckgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchRegionAndHTMLResults(t *testing.T) {
	limit := 3
	options := &searcher.SearchOptions{Limit: &limit, Location: " ch "}
	want := *options
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "html.duckduckgo.com" || r.URL.Path != "/html/" || r.Method != http.MethodGet || r.URL.Query().Get("q") != "release" || r.URL.Query().Get("kl") != "ch-de" {
			t.Fatalf("wrong request: %s", r.URL)
		}
		body := `<html><body>
<div class="result results_links"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1%26y%3D2&amp;rut=hash">Research &amp; <b>release</b></a><a class="result__snippet">Evidence &lt;verbatim&gt;.</a></div>
<div class="result"><a class="result__a" href="https://example.org/b">No snippet</a></div>
<div class="result result--ad"><a class="result__a" href="https://ad.example/">Advertisement</a><a class="result__snippet">Ad</a></div>
<div class="result"><a class="result__a" href="javascript:alert(1)">Invalid URL</a><a class="result__snippet">Ignore</a></div>
<div class="result"><a class="result__a" href="https://example.net/c">Long result</a><a class="result__snippet">` + strings.Repeat("a", 70000) + `</a></div>
</body></html>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	c, _ := New(WithClient(httpClient))
	results, err := c.Search(context.Background(), " release ", options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*options, want) {
		t.Fatal("mutated caller options")
	}
	if len(results) != 3 || results[0].Source != "https://example.com/a?x=1&y=2" || results[0].Title != "Research & release" || results[0].Content != "Evidence <verbatim>." || results[1].Content != "" || len(results[2].Content) != 70000 {
		t.Fatalf("wrong results: %d %+v", len(results), results[:min(len(results), 2)])
	}
}

func TestSearchDomainUnionExclusionsAndLimit(t *testing.T) {
	limit := 2
	options := &searcher.SearchOptions{Limit: &limit, Include: []string{"example.com", "example.org"}, Exclude: []string{"blocked.example.com"}, Location: "GB"}
	var queries []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		queries = append(queries, r.URL.Query().Get("q"))
		if r.URL.Query().Get("kl") != "uk-en" {
			t.Fatal("wrong region")
		}
		body := `<div class="result"><a class="result__a" href="https://blocked.example.com/ad">Excluded</a></div><div class="result"><a class="result__a" href="https://other.example/no">Related fallback</a></div>`
		if strings.HasSuffix(r.URL.Query().Get("q"), "site:example.com") {
			body += `<div class="result"><a class="result__a" href="https://example.com/a">First</a></div>`
		} else {
			body += `<div class="result"><a class="result__a" href="https://example.com/a">Duplicate</a></div><div class="result"><a class="result__a" href="https://docs.example.org/b">Second</a></div><div class="result"><a class="result__a" href="https://example.org/c">Over limit</a></div>`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	c, _ := New(WithClient(httpClient))
	results, err := c.Search(context.Background(), "release", options)
	if err != nil {
		t.Fatal(err)
	}
	wantQueries := []string{"release -site:blocked.example.com site:example.com", "release -site:blocked.example.com site:example.org"}
	if !reflect.DeepEqual(queries, wantQueries) {
		t.Fatalf("queries = %v", queries)
	}
	if len(results) != 2 || results[0].Source != "https://example.com/a" || results[1].Source != "https://docs.example.org/b" {
		t.Fatalf("wrong results: %#v", results)
	}
}

func TestSearchRejectsInvalidAndUnsupportedOptions(t *testing.T) {
	c, _ := New(WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid options must not make an HTTP request")
		return nil, nil
	})}))
	if _, err := c.Search(context.Background(), " ", nil); err == nil {
		t.Fatal("accepted empty query")
	}
	zero, negative := 0, -1
	date := time.Now()
	for _, options := range []searcher.SearchOptions{
		{Limit: &zero}, {Limit: &negative}, {Category: "news"}, {Location: "ZZ"}, {Since: &date}, {Until: &date},
		{Include: []string{"example.com OR site:spam.example"}}, {Exclude: []string{"https://example.com/path"}},
	} {
		if _, err := c.Search(context.Background(), "query", &options); err == nil {
			t.Errorf("accepted %+v", options)
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestSearchPropagatesHTTPErrorsAndReadFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   io.Reader
		want   string
	}{
		{http.StatusTooManyRequests, strings.NewReader(""), "HTTP 429"},
		{http.StatusAccepted, strings.NewReader("challenge"), "HTTP 202"},
		{http.StatusOK, errorReader{}, "read failed"},
	} {
		c, _ := New(WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(tc.body)}, nil
		})}))
		if _, err := c.Search(context.Background(), "query", nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("got %v, want %s", err, tc.want)
		}
	}
}
