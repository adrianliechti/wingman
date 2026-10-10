package exa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	searchtool "github.com/adrianliechti/wingman/pkg/tool/search"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchRetrievalOnlyAndOptionsUnchanged(t *testing.T) {
	limit := 3
	options := &searcher.SearchOptions{Limit: &limit, Include: []string{"example.com"}, Exclude: []string{"excluded.example"}}
	want := *options
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.exa.ai/search" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["type"] != "fast" || body["query"] != "launch date" || body["numResults"] != float64(3) || body["category"] != "news" || body["userLocation"] != "CH" {
			t.Fatalf("wrong request: %#v", body)
		}
		if !reflect.DeepEqual(body["contents"], map[string]any{"text": true}) {
			t.Fatalf("only plain text retrieval is allowed: %#v", body["contents"])
		}
		for _, name := range []string{"outputSchema", "summary", "highlights", "systemPrompt", "additionalQueries"} {
			if _, ok := body[name]; ok {
				t.Fatalf("unexpected synthesis field %s", name)
			}
		}
		if r.Header.Get("x-api-key") != "test-token" {
			t.Fatal("missing authentication")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com","title":"Report","text":"Evidence","publishedDate":"2031-05-14T09:00:00Z"}]}`))}, nil
	})}
	c, err := New("test-token", WithClient(httpClient), WithCategory("news"), WithLocation("CH"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := c.Search(context.Background(), " launch date ", options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*options, want) {
		t.Fatalf("mutated caller options: %#v", options)
	}
	if len(results) != 1 || results[0].Content != "Evidence" || results[0].Timestamp == nil || results[0].Metadata["published"] != "2031-05-14T09:00:00Z" {
		t.Fatalf("missing evidence or publication date: %#v", results)
	}
}

func TestSearchRejectsSynthesisModesAndInvalidInput(t *testing.T) {
	for _, mode := range []string{"deep", "deep-lite", "deep-reasoning", "unknown"} {
		if _, err := New("test", WithMode(mode)); err == nil {
			t.Errorf("accepted mode %s", mode)
		}
	}
	c, err := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input must not make a request")
		return nil, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "  ", nil); err == nil {
		t.Error("accepted empty query")
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err := c.Search(context.Background(), "query", &searcher.SearchOptions{Limit: &limit}); err == nil {
			t.Errorf("accepted limit %d", limit)
		}
	}
}

func TestSearchExcerptFindsLateEvidenceWithoutSynthesizing(t *testing.T) {
	page := strings.Repeat("Routine operations and background. ", 800) + "\nLaunch date: 14 May 2031.\n" + strings.Repeat("Appendix. ", 1000)
	got := searchExcerpt(page, "launch date")
	if !strings.Contains(got, "Launch date: 14 May 2031.") {
		t.Fatal("lost late evidence")
	}
	if len([]rune(got)) > 1500 {
		t.Fatal("excerpt exceeds the frontend snippet budget")
	}
	_, body, _ := strings.Cut(got, "\n")
	if !strings.Contains(page, body) {
		t.Fatal("excerpt was not verbatim")
	}
}

func TestSearchExcerptUnicodeAndMissingMatches(t *testing.T) {
	page := strings.Repeat("背景。", 1500) + "公開日：2031年5月14日。" + strings.Repeat("付録。", 800)
	if got := searchExcerpt(page, "公開日"); !strings.Contains(got, "公開日：2031年5月14日。") {
		t.Fatal("lost Unicode evidence")
	}
	if got := searchExcerpt("  Short text.\n", "other"); got != "  Short text.\n" {
		t.Fatal("changed short text")
	}
	if got := searchExcerpt(strings.Repeat("a", 2000), "missing"); !strings.HasSuffix(got, strings.Repeat("a", excerptCharacters)) {
		t.Fatal("missing prefix fallback")
	}
}

func TestSearchPublicationDateBounds(t *testing.T) {
	since := time.Date(2031, 5, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2031, 5, 31, 0, 0, 0, 0, time.UTC)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["startPublishedDate"] != "2031-05-01T00:00:00Z" || body["endPublishedDate"] != "2031-05-31T00:00:00Z" {
			t.Fatalf("missing date bounds: %#v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
	})}
	c, err := New("test-token", WithClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "launch", &searcher.SearchOptions{Since: &since, Until: &until}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "launch", &searcher.SearchOptions{Since: &until, Until: &since}); err == nil {
		t.Fatal("accepted inverted date bounds")
	}
}

func TestSearchPublicationCategoryAndLegacyAlias(t *testing.T) {
	for _, category := range []string{CategoryPublication, CategoryResearchPaper, " Research Paper "} {
		t.Run(category, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body SearchRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Category != "publication" {
					t.Fatalf("category = %q", body.Category)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
			})}
			c, err := New("test", WithClient(httpClient), WithCategory(category))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Search(context.Background(), "papers", nil); err != nil {
				t.Fatal(err)
			}
			options := &searcher.SearchOptions{Category: category}
			if _, err := c.Search(context.Background(), "papers", options); err != nil {
				t.Fatal(err)
			}
			if options.Category != category {
				t.Fatal("mutated caller category")
			}
		})
	}
	c, _ := New("test")
	var publication bool
	for _, category := range c.Capabilities().Categories {
		publication = publication || category.Name == "publication"
		if category.Name == "research paper" {
			t.Fatal("advertised the legacy category")
		}
	}
	if !publication {
		t.Fatal("missing publication category")
	}
}

func TestSearchRejectsUnsupportedEntityFilters(t *testing.T) {
	date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported filters must be rejected before HTTP")
		return nil, nil
	})}
	for _, category := range []string{"company", "people", " People "} {
		for _, options := range []searcher.SearchOptions{
			{Since: &date}, {Until: &date}, {Exclude: []string{"spam.example"}},
		} {
			c, _ := New("test", WithClient(httpClient), WithCategory(category))
			want := options
			if _, err := c.Search(context.Background(), "founder", &options); err == nil || !strings.Contains(err.Error(), "does not support") {
				t.Fatalf("category %q: got %v", category, err)
			}
			if !reflect.DeepEqual(options, want) {
				t.Fatal("mutated caller options")
			}
		}
	}
}

func TestSearchToolInclusiveUntilAndExactTimestamp(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"2026-10-10", "2026-10-11T00:00:00Z"},
		{"2026-10-10T00:00:00Z", "2026-10-10T00:00:00Z"},
		{"2026-10-10T12:30:00.123+02:00", "2026-10-10T10:30:00.123Z"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body SearchRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.EndPublishedDate != tc.want {
					t.Fatalf("until = %q, want %q", body.EndPublishedDate, tc.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
			})}
			p, _ := New("test", WithClient(httpClient))
			tool, _ := searchtool.New(p)
			if _, err := tool.Execute(context.Background(), searchtool.ToolName, map[string]any{"query": "release", "until": tc.input}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchRejectsInvalidDomainsLocationAndEmptyWindow(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid options must not make an HTTP request")
		return nil, nil
	})}
	c, _ := New("test", WithClient(httpClient))
	date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, options := range []searcher.SearchOptions{
		{Include: make([]string, 1201)}, {Exclude: make([]string, 1201)},
		{Location: "Switzerland"}, {Location: "1A"}, {Since: &date, Until: &date},
	} {
		if _, err := c.Search(context.Background(), "query", &options); err == nil {
			t.Errorf("accepted %+v", options)
		}
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestSearchResponsesAndBodyClosure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"empty", http.StatusOK, `{"results":[]}`, false},
		{"missing results", http.StatusOK, `{}`, true},
		{"null results", http.StatusOK, `{"results":null}`, true},
		{"malformed", http.StatusOK, `invalid`, true},
		{"provider error", http.StatusTooManyRequests, `{"error":"rate limited"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(tc.body)}
			c, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			})}))
			_, err := c.Search(context.Background(), "query", nil)
			if (err != nil) != tc.wantError || !body.closed {
				t.Fatalf("error = %v, closed = %v", err, body.closed)
			}
			if tc.status != http.StatusOK && !strings.Contains(err.Error(), "rate limited") {
				t.Fatalf("lost provider error: %v", err)
			}
		})
	}
}
