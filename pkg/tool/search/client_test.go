package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"
	"github.com/adrianliechti/wingman/pkg/searcher/custom"
	"github.com/adrianliechti/wingman/pkg/searcher/duckduckgo"
	"github.com/adrianliechti/wingman/pkg/searcher/exa"
	"github.com/adrianliechti/wingman/pkg/searcher/tavily"
)

type fakeSearcher struct {
	query      string
	options    *searcher.SearchOptions
	results    []searcher.Result
	err        error
	categories []searcher.Category
}

func (f *fakeSearcher) Search(ctx context.Context, q string, o *searcher.SearchOptions) ([]searcher.Result, error) {
	f.query = q
	f.options = o
	return f.results, f.err
}

func (f *fakeSearcher) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{DateFilters: true, Categories: f.categories}
}

func TestNew_RequiresProvider(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error when searcher is nil")
	}
}

func TestTools_SchemaShape(t *testing.T) {
	c, _ := New(&fakeSearcher{})

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != ToolName {
		t.Fatalf("expected one tool named %q; got %+v", ToolName, tools)
	}

	props, _ := tools[0].Parameters["properties"].(map[string]any)
	for _, key := range []string{"query", "queries", "allowed_domains", "blocked_domains"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
	if _, ok := props["category"]; ok {
		t.Errorf("category property should be absent when searcher exposes no categories")
	}
	if got, _ := tools[0].Parameters["required"].([]string); len(got) != 0 {
		t.Errorf("required = %v; query and queries are alternatives", got)
	}
}

func TestTools_CategorySchemaFromProvider(t *testing.T) {
	c, _ := New(&fakeSearcher{
		categories: []searcher.Category{
			{Name: "news", Description: "Recent articles."},
			{Name: "code", Description: "GitHub and StackOverflow."},
		},
	})

	tools, _ := c.Tools(context.Background())
	props, _ := tools[0].Parameters["properties"].(map[string]any)
	cat, ok := props["category"].(map[string]any)
	if !ok {
		t.Fatalf("expected category property; got %v", props["category"])
	}
	if got := cat["enum"]; !reflect.DeepEqual(got, []string{"news", "code"}) {
		t.Errorf("category enum = %v, want [news code]", got)
	}
	desc, _ := cat["description"].(string)
	for _, want := range []string{"news", "Recent articles.", "code", "GitHub and StackOverflow."} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestExecute_PassesDomainsAndLocation(t *testing.T) {
	f := &fakeSearcher{
		results: []searcher.Result{
			{Source: "https://go.dev/blog/go1.24", Title: "Go 1.24", Content: "Body of post"},
		},
	}
	c, _ := New(f, WithLimit(3))

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"query":           "go release",
		"category":        "code",
		"location":        "CH",
		"allowed_domains": []any{"go.dev"},
		"blocked_domains": []any{"medium.com"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if f.query != "go release" {
		t.Errorf("query = %q", f.query)
	}
	if f.options.Category != "code" {
		t.Errorf("category = %q", f.options.Category)
	}
	if f.options.Location != "CH" {
		t.Errorf("location = %q", f.options.Location)
	}
	if !reflect.DeepEqual(f.options.Include, []string{"go.dev"}) {
		t.Errorf("include = %v", f.options.Include)
	}
	if !reflect.DeepEqual(f.options.Exclude, []string{"medium.com"}) {
		t.Errorf("exclude = %v", f.options.Exclude)
	}
	if f.options.Limit == nil || *f.options.Limit != 3 {
		t.Errorf("limit = %v", f.options.Limit)
	}

	text, ok := got.(string)
	if !ok {
		t.Fatalf("Execute returned %T, want string", got)
	}
	for _, want := range []string{"https://go.dev/blog/go1.24", "Go 1.24", "Found 1 result"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
}

func TestExecute_WrongTool(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	if _, err := c.Execute(context.Background(), "wrong", nil); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestExecute_MissingQuery(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{}); err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestExecute_MaxResultsOverridesLimit(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := New(f, WithLimit(5))

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{
		"query":       "go release",
		"max_results": float64(8),
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.options.Limit == nil || *f.options.Limit != 8 {
		t.Errorf("limit = %v, want 8", f.options.Limit)
	}

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{
		"query":       "go release",
		"max_results": float64(50),
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.options.Limit == nil || *f.options.Limit != 10 {
		t.Errorf("limit = %v, want clamped to 10", f.options.Limit)
	}
}

func TestFormatResults_Timestamp(t *testing.T) {
	ts := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	got := formatResults([]searcher.Result{
		{Source: "https://go.dev/x", Title: "Go", Content: "body", Timestamp: &ts},
	}, 400)
	if !strings.Contains(got, "— 2026-05-01\n") {
		t.Errorf("missing timestamp in:\n%s", got)
	}
}

func TestExecute_PropagatesSearcherError(t *testing.T) {
	want := errors.New("backend down")
	c, _ := New(&fakeSearcher{err: want})
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x"}); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestExecute_EmptyResults(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	got, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "nothing"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.(string) != "No results." {
		t.Errorf("got %q", got)
	}
}

func TestResult_PassesThroughText(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	out := c.Result(ToolName, "some markdown")
	if len(out.Parts) != 1 || out.Parts[0].Text != "some markdown" {
		t.Errorf("got %+v", out)
	}
}

func TestExecute_ConfigurableExcerptPreservesLateEvidence(t *testing.T) {
	f := &fakeSearcher{results: []searcher.Result{{Content: strings.Repeat("background ", 60) + "Calibration: VX-7319."}}}
	c, _ := New(f, WithMaxSnippetChars(1500))
	got, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "calibration"})
	if err != nil || !strings.Contains(got.(string), "VX-7319") {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestExecute_DateBounds(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := New(f)

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{
		"query": "go release",
		"since": "2026-05-01",
		"until": "2026-05-31T23:59:59Z",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.options.Since == nil || !f.options.Since.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v", f.options.Since)
	}
	if f.options.Until == nil || !f.options.Until.Equal(time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("until = %v", f.options.Until)
	}

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "since": "last week"}); err == nil {
		t.Fatal("expected error for an unparsable date")
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "since": 123}); err == nil {
		t.Fatal("expected error for a non-string date")
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "since": "2026-10-10", "until": "2026-10-10"}); err != nil {
		t.Fatal(err)
	}
	if f.options.Until == nil || !f.options.Until.Equal(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date-only until does not include the full day: %v", f.options.Until)
	}

	tools, _ := c.Tools(context.Background())
	props, _ := tools[0].Parameters["properties"].(map[string]any)
	for _, key := range []string{"since", "until"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
}

type batchSearcher struct {
	mu      sync.Mutex
	queries []string
	options []*searcher.SearchOptions
	results map[string][]searcher.Result
}

func (f *batchSearcher) Search(ctx context.Context, q string, o *searcher.SearchOptions) ([]searcher.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.options = append(f.options, o)
	if q == "broken" {
		return nil, errors.New("backend down")
	}
	return f.results[q], nil
}

func (f *batchSearcher) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{DateFilters: true}
}

func TestExecute_BatchesQueriesAndDeduplicates(t *testing.T) {
	f := &batchSearcher{results: map[string][]searcher.Result{
		"a": {{Source: "https://www.example.com/page/", Title: "Page", Content: "A"}},
		"b": {{Source: "https://example.com/page#top", Title: "Page", Content: "A"}, {Source: "https://example.com/other", Title: "Other", Content: "C"}},
	}}
	c, _ := New(f)

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"queries": []any{"a", " b ", "a", "broken"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := got.(string)
	sort.Strings(f.queries)
	if !reflect.DeepEqual(f.queries, []string{"a", "b", "broken"}) {
		t.Errorf("queries = %v", f.queries)
	}
	for _, want := range []string{"## Query: a", "## Query: b", "## Query: broken", "Error: backend down", "https://example.com/other", "1 result(s) already listed above omitted."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
	if strings.Count(text, "[Page]") != 1 {
		t.Errorf("duplicate URL listed twice:\n%s", text)
	}

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"queries": []any{"broken"}}); err == nil {
		t.Fatal("expected error when every query fails")
	}
	many := make([]any, maxQueries+1)
	for i := range many {
		many[i] = fmt.Sprint("q", i)
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"queries": many}); err == nil {
		t.Fatal("expected error for too many queries")
	}
}

func TestTools_ReflectProviderCapabilities(t *testing.T) {
	for _, test := range []struct {
		name       string
		provider   searcher.Provider
		dates      bool
		categories []string
	}{
		{"exa", &exa.Client{}, true, []string{"company", "people", "news", "publication", "personal site", "financial report"}},
		{"tavily", &tavily.Client{}, true, []string{"general", "news", "finance"}},
		{"duckduckgo", &duckduckgo.Client{}, false, nil},
		{"custom", &custom.Client{}, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := New(test.provider)
			tools, err := c.Tools(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			props := tools[0].Parameters["properties"].(map[string]any)
			for _, field := range []string{"recency", "since", "until"} {
				if _, present := props[field]; present != test.dates {
					t.Fatalf("%s present = %v", field, present)
				}
			}
			category, present := props["category"]
			if present != (len(test.categories) > 0) {
				t.Fatalf("category present = %v, want %v", present, len(test.categories) > 0)
			}
			if category, ok := category.(map[string]any); ok {
				if !reflect.DeepEqual(category["enum"], test.categories) {
					t.Fatalf("category enum = %v, want %v", category["enum"], test.categories)
				}
			} else if present {
				t.Fatalf("invalid category schema: %v", category)
			}
			if len(tools[0].Parameters["anyOf"].([]map[string]any)) != 2 {
				t.Fatal("query alternatives missing")
			}
			if !test.dates {
				_, err := c.Execute(t.Context(), ToolName, map[string]any{"query": "q", "since": "2026-01-01"})
				if err == nil || !strings.Contains(err.Error(), "does not support") {
					t.Fatalf("date error = %v", err)
				}
			}
		})
	}
}

func TestExecute_NativeParametersAndMalformedFilters(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := New(f, WithLimit(50))
	if _, err := c.Execute(t.Context(), ToolName, map[string]any{
		"queries": []string{"q"}, "max_results": 7,
		"allowed_domains": []string{" go.dev ", "go.dev"},
	}); err != nil || f.query != "q" || *f.options.Limit != 7 || !reflect.DeepEqual(f.options.Include, []string{"go.dev"}) {
		t.Fatalf("options %+v, err %v", f.options, err)
	}
	if _, err := c.Execute(t.Context(), ToolName, map[string]any{"query": "q"}); err != nil || *f.options.Limit != 10 {
		t.Fatalf("configured limit %+v, err %v", f.options, err)
	}
	for _, params := range []map[string]any{
		{"query": 42}, {"queries": "q"}, {"queries": []any{"q", 42}},
		{"query": "q", "max_results": 1.5}, {"query": "q", "max_results": 0},
		{"query": "q", "max_results": math.Inf(1)}, {"query": "q", "max_results": "3"},
		{"query": "q", "category": []any{"news"}}, {"query": "q", "location": 42},
		{"query": "q", "recency": true}, {"query": "q", "allowed_domains": "go.dev"},
		{"query": "q", "blocked_domains": []any{"spam.example", 42}},
	} {
		f.query = ""
		if _, err := c.Execute(t.Context(), ToolName, params); err == nil || f.query != "" {
			t.Fatalf("invalid request reached provider: %v, err %v", params, err)
		}
	}
}

func TestExecute_BatchRetainsDifferentEvidenceFromSameSource(t *testing.T) {
	date := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	f := &batchSearcher{results: map[string][]searcher.Result{
		"launch": {{Source: "https://example.com/report", Title: "Report", Content: "Launch: May 14."}},
		"budget": {{Source: "https://example.com/report", Title: "Report", Content: "Budget: 83 million."}},
		"dated":  {{Source: "https://example.com/report", Title: "Report", Content: "Launch: May 14.", Timestamp: &date}},
	}}
	c, _ := New(f)
	got, err := c.Execute(t.Context(), ToolName, map[string]any{"queries": []string{"launch", "budget", "dated"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Launch: May 14.", "Budget: 83 million.", "2026-05-01"} {
		if !strings.Contains(got.(string), text) {
			t.Fatalf("lost evidence %q: %s", text, got)
		}
	}
	if strings.Contains(got.(string), "omitted") {
		t.Fatalf("discarded distinct evidence: %s", got)
	}
}

func TestExecute_RecencyShortcut(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := New(f)
	c.now = func() time.Time { return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) }

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "recency": "month"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if f.options.Since == nil || !f.options.Since.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v", f.options.Since)
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "recency": "week", "since": "2026-01-01"}); err == nil {
		t.Fatal("expected error for recency with since")
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "recency": "decade"}); err == nil {
		t.Fatal("expected error for unknown recency")
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x", "since": "2026-02-01", "until": "2026-01-01"}); err == nil {
		t.Fatal("expected error for inverted bounds")
	}
	tools, _ := c.Tools(context.Background())
	props, _ := tools[0].Parameters["properties"].(map[string]any)
	for _, key := range []string{"queries", "recency"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
}
