package search

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/searcher"
)

type fakeSearcher struct {
	query   string
	options *searcher.SearchOptions
	results []searcher.Result
	err     error
}

func (f *fakeSearcher) Search(ctx context.Context, q string, o *searcher.SearchOptions) ([]searcher.Result, error) {
	f.query = q
	f.options = o
	return f.results, f.err
}

func TestNew_RequiresProvider(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error when searcher is nil")
	}
}

func TestTools_SchemaShape(t *testing.T) {
	c, err := New(&fakeSearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != ToolName {
		t.Fatalf("expected one tool named %q; got %+v", ToolName, tools)
	}

	props, _ := tools[0].Parameters["properties"].(map[string]any)
	for _, key := range []string{"query", "allowed_domains", "blocked_domains"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q in schema", key)
		}
	}

	required, _ := tools[0].Parameters["required"].([]string)
	if !reflect.DeepEqual(required, []string{"query"}) {
		t.Errorf("required = %v, want [query]", required)
	}
}

func TestExecute_PassesDomainsAndLocation(t *testing.T) {
	f := &fakeSearcher{
		results: []searcher.Result{
			{Source: "https://go.dev/blog/go1.24", Title: "Go 1.24", Content: "Body of post"},
		},
	}
	c, err := New(f, WithLimit(3), WithLocation("Zurich, CH"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"query":           "go release",
		"allowed_domains": []any{"go.dev"},
		"blocked_domains": []any{"medium.com"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if f.query != "go release" {
		t.Errorf("query = %q, want %q", f.query, "go release")
	}
	if f.options.Location != "Zurich, CH" {
		t.Errorf("location = %q, want %q", f.options.Location, "Zurich, CH")
	}
	if !reflect.DeepEqual(f.options.Include, []string{"go.dev"}) {
		t.Errorf("include = %v, want [go.dev]", f.options.Include)
	}
	if !reflect.DeepEqual(f.options.Exclude, []string{"medium.com"}) {
		t.Errorf("exclude = %v, want [medium.com]", f.options.Exclude)
	}
	if f.options.Limit == nil || *f.options.Limit != 3 {
		t.Errorf("limit not propagated; got %v", f.options.Limit)
	}

	results, ok := got.([]Result)
	if !ok || len(results) != 1 {
		t.Fatalf("got = %T %v", got, got)
	}
	if results[0].URL != "https://go.dev/blog/go1.24" {
		t.Errorf("URL = %q", results[0].URL)
	}
}

func TestExecute_WrongTool(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	if _, err := c.Execute(context.Background(), "wrong", nil); err == nil {
		t.Fatal("expected error for unknown tool name")
	}
}

func TestExecute_MissingQuery(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{}); err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestExecute_PropagatesSearcherError(t *testing.T) {
	want := errors.New("backend down")
	c, _ := New(&fakeSearcher{err: want})
	_, err := c.Execute(context.Background(), ToolName, map[string]any{"query": "x"})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestResult_FormatsMarkdown(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	out := c.Result(ToolName, []Result{
		{URL: "https://a.example/x", Title: "A", Snippet: "first"},
		{URL: "https://b.example/y", Title: "B", Snippet: "second"},
	})
	if len(out.Parts) != 1 {
		t.Fatalf("parts = %v", out.Parts)
	}
	text := out.Parts[0].Text
	for _, want := range []string{"https://a.example/x", "https://b.example/y", "first", "second"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
}

func TestResult_Empty(t *testing.T) {
	c, _ := New(&fakeSearcher{})
	out := c.Result(ToolName, []Result{})
	if out.Parts[0].Text != "No results." {
		t.Errorf("got %q", out.Parts[0].Text)
	}
}
