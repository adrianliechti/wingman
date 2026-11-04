package exa

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/tool/search"
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
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body["type"] != "auto" || body["query"] != "launch date" || body["numResults"] != float64(3) || body["category"] != "news" || body["userLocation"] != "CH" {
			t.Fatalf("wrong request: %#v", body)
		}
		if !reflect.DeepEqual(body["contents"], map[string]any{"highlights": map[string]any{"dynamic": true}}) {
			t.Fatalf("default search must retrieve excerpts without full text: %#v", body["contents"])
		}
		if r.Header.Get("Exa-Beta") != "dynamic-highlights-2026-08-28" {
			t.Fatal("missing dynamic highlights header")
		}
		for _, name := range []string{"outputSchema", "summary", "highlights", "systemPrompt", "additionalQueries"} {
			if _, ok := body[name]; ok {
				t.Fatalf("unexpected synthesis field %s", name)
			}
		}
		if r.Header.Get("x-api-key") != "test-token" {
			t.Fatal("missing authentication")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com","title":"Report","highlights":["Evidence"],"publishedDate":"2031-05-14T09:00:00Z"}]}`))}, nil
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
	if len(results) != 1 || results[0].Excerpt != "Evidence" || results[0].Text != nil || results[0].Timestamp == nil || results[0].Metadata["published"] != "2031-05-14T09:00:00Z" {
		t.Fatalf("missing evidence or publication date: %#v", results)
	}
}

func TestSearchRetainsFullTextIncludingFailedRetrievals(t *testing.T) {
	page := strings.Repeat("Full source evidence. ", 200)
	body, _ := json.Marshal(map[string]any{"results": []map[string]any{
		{"url": "https://example.com/full", "title": "Full", "text": page, "highlights": []string{"Selected evidence"}},
		{"url": "https://example.com/redundant", "title": "Redundant", "text": page, "highlights": []string{}},
		{"url": "https://example.com/empty", "title": "Empty"},
	}})
	c, err := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request SearchRequest
		if err := json.UnmarshalRead(r.Body, &request); err != nil {
			t.Fatal(err)
		}
		if request.Contents == nil || !request.Contents.Text || request.Contents.Highlights == nil || !request.Contents.Highlights.Dynamic {
			t.Fatal("full text must be requested alongside excerpts")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := c.Search(t.Context(), "source evidence", &searcher.SearchOptions{Text: &searcher.TextOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Text == nil || *results[0].Text != page || results[0].Excerpt != "Selected evidence" {
		t.Fatal("full text was lost or replaced the bounded excerpt")
	}
	if results[1].Excerpt != "" || results[1].Text == nil || *results[1].Text != page {
		t.Fatal("locally generated excerpts defeated dynamic highlight selection")
	}
	if results[2].Text == nil || *results[2].Text != "" {
		t.Fatal("unavailable text was mistaken for unattempted retrieval")
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

func TestSearchRetrievalModesAndCompanyExclusions(t *testing.T) {
	for _, mode := range []string{"auto", "lite", "fast", "instant"} {
		t.Run(mode, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body["type"] != mode || body["category"] != "company" || body["numResults"] != float64(100) {
					t.Fatalf("unexpected request: %#v", body)
				}
				if !reflect.DeepEqual(body["excludeDomains"], []any{"spam.example"}) {
					t.Fatal("company domain exclusions were lost")
				}
				if !reflect.DeepEqual(body["contents"], map[string]any{"highlights": map[string]any{"dynamic": true}}) || r.Header.Get("Exa-Beta") != "dynamic-highlights-2026-08-28" {
					t.Fatalf("dynamic highlights are not enabled: %#v", body["contents"])
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
			})}
			c, err := New("test", WithClient(httpClient), WithMode(mode))
			if err != nil {
				t.Fatal(err)
			}
			limit := 100
			options := &searcher.SearchOptions{Limit: &limit, Category: " Company ", Exclude: []string{"spam.example"}}
			if _, err := c.Search(t.Context(), "software companies", options); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchPublicationDateBounds(t *testing.T) {
	since := time.Date(2031, 5, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2031, 5, 31, 0, 0, 0, 0, time.UTC)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
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
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
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
	for _, category := range c.SearchCapabilities().Categories {
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
			if category == "company" && len(options.Exclude) > 0 {
				continue // Company search supports domain exclusions.
			}
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
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body.EndPublishedDate != tc.want {
					t.Fatalf("until = %q, want %q", body.EndPublishedDate, tc.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
			})}
			p, _ := New("test", WithClient(httpClient))
			tool, _ := search.New(p)
			if _, err := tool.Execute(context.Background(), search.ToolName, map[string]any{"query": "release", "until": tc.input}); err != nil {
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
