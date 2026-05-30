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

func (f *fakeSearcher) Categories() []searcher.Category {
	return f.categories
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
	for _, key := range []string{"query", "allowed_domains", "blocked_domains"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
	if _, ok := props["category"]; ok {
		t.Errorf("category property should be absent when searcher exposes no categories")
	}
}

func TestTools_CategoryNoEnum(t *testing.T) {
	c, _ := New(&fakeSearcher{
		categories: []searcher.Category{{Name: "news", Description: "Recent articles."}},
	})

	tools, _ := c.Tools(context.Background())
	props, _ := tools[0].Parameters["properties"].(map[string]any)
	cat, _ := props["category"].(map[string]any)

	if _, has := cat["enum"]; has {
		t.Errorf("category should not have an enum constraint (Exa accepts any string)")
	}
	if got, _ := tools[0].Parameters["required"].([]string); !reflect.DeepEqual(got, []string{"query"}) {
		t.Errorf("required = %v, want [query]", got)
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
