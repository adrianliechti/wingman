package tavily

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
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchTopicsDatesAndPublication(t *testing.T) {
	limit := 4
	since := time.Date(2031, 5, 1, 0, 0, 0, 0, time.UTC)
	options := &searcher.SearchOptions{Limit: &limit, Category: "news", Since: &since, Include: []string{"example.com"}, Exclude: []string{"spam.example"}}
	want := *options
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.tavily.com/search" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["topic"] != "news" || body["start_date"] != "2031-05-01" || body["max_results"] != float64(4) || body["include_published_date"] != true || body["filter_by_published_date"] != true {
			t.Fatalf("wrong request: %#v", body)
		}
		if _, ok := body["end_date"]; ok {
			t.Fatalf("unexpected end_date: %#v", body)
		}
		if !reflect.DeepEqual(body["include_domains"], []any{"example.com"}) || !reflect.DeepEqual(body["exclude_domains"], []any{"spam.example"}) {
			t.Fatalf("wrong domains: %#v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com/a","title":"Report","content":"Evidence","published_date":"Wed, 14 May 2031 09:00:00 GMT"},{"url":"https://example.com/b","title":"Page","content":"More"}]}`))}, nil
	})}
	c, err := New("test-token", WithClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	results, err := c.Search(context.Background(), "launch date", options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*options, want) {
		t.Fatalf("mutated caller options: %#v", options)
	}
	if len(results) != 1 || results[0].Timestamp == nil || results[0].Metadata["published"] != "2031-05-14T09:00:00Z" {
		t.Fatalf("missing publication date: %#v", results)
	}
}

func TestTopicMapping(t *testing.T) {
	for input, want := range map[string]string{"": "general", "general": "general", "News": "news", "finance": "finance", "financial report": "finance"} {
		if got, err := topic(input); err != nil || got != want {
			t.Errorf("topic(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := topic("research paper"); err == nil {
		t.Fatal("accepted unsupported category")
	}
	if len((&Client{}).Capabilities().Categories) != 3 {
		t.Fatal("expected general, news and finance categories")
	}
}

func TestSearchGeneralCountryAndOptionalDates(t *testing.T) {
	for code, country := range map[string]string{" ch ": "switzerland", "US": "united states", "GB": "united kingdom", "CZ": "czech republic", "CV": "cape verde", "MM": "myanmar"} {
		t.Run(code, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["country"] != country || body["topic"] != "general" || body["include_published_date"] != true {
					t.Fatalf("wrong request: %#v", body)
				}
				if _, ok := body["filter_by_published_date"]; ok {
					t.Fatal("unbounded search must keep undated results")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com/a","published_date":"2031-05-14T09:00:00Z"},{"url":"https://example.com/b","published_date":null}]}`))}, nil
			})}
			c, _ := New("test", WithClient(httpClient))
			results, err := c.Search(context.Background(), "query", &searcher.SearchOptions{Location: code})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 || results[0].Timestamp == nil || results[1].Timestamp != nil || results[1].Metadata != nil {
				t.Fatalf("wrong dates: %#v", results)
			}
		})
	}
}

func TestSearchRejectsInvalidAndUnsupportedOptions(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid options must not make an HTTP request")
		return nil, nil
	})}
	c, _ := New("test", WithClient(httpClient))
	if _, err := c.Search(context.Background(), " ", nil); err == nil {
		t.Fatal("accepted empty query")
	}
	date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	later := date.AddDate(0, 0, 1)
	negative, excessive := -1, 21
	for _, options := range []searcher.SearchOptions{
		{Limit: &negative}, {Limit: &excessive}, {Include: make([]string, 301)}, {Exclude: make([]string, 151)},
		{Category: "publication"}, {Location: "ZZ"}, {Location: "Switzerland"},
		{Category: "news", Location: "CH"}, {Category: "finance", Location: "US"},
		{Since: &date, Until: &date}, {Since: &later, Until: &date},
	} {
		if _, err := c.Search(context.Background(), "query", &options); err == nil {
			t.Errorf("accepted %+v", options)
		}
	}
}

func TestSearchExactDateWindow(t *testing.T) {
	since, _ := time.Parse(time.RFC3339, "2026-10-10T09:00:00Z")
	until, _ := time.Parse(time.RFC3339, "2026-10-10T12:00:00Z")
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["start_date"] != "2026-10-10" || body["end_date"] != "2026-10-11" || body["filter_by_published_date"] != true {
			t.Fatalf("wrong window: %#v", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com/early","published_date":"2026-10-10T08:59:59Z"},{"url":"https://example.com/start","published_date":"2026-10-10T09:00:00Z"},{"url":"https://example.com/end","published_date":"2026-10-10T12:00:00Z"},{"url":"https://example.com/unknown","published_date":null},{"url":"https://example.com/invalid","published_date":"unknown"}]}`))}, nil
	})}
	c, _ := New("test", WithClient(httpClient))
	results, err := c.Search(context.Background(), "query", &searcher.SearchOptions{Since: &since, Until: &until})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "https://example.com/start" {
		t.Fatalf("wrong results: %#v", results)
	}
}

func TestSearchAllowsDocumentedResultLimits(t *testing.T) {
	for _, limit := range []int{0, 20} {
		c, _ := New("test", WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["max_results"] != float64(limit) {
				t.Fatalf("wrong limit: %#v", body)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
		})}))
		if _, err := c.Search(context.Background(), "query", &searcher.SearchOptions{Limit: &limit}); err != nil {
			t.Fatal(err)
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
		{"provider error", http.StatusTooManyRequests, `{"detail":{"error":"rate limited"}}`, true},
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
