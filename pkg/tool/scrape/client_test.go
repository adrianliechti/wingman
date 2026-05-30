package scrape

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/scraper"
)

type fakeScraper struct {
	url string
	doc *scraper.Document
	err error
}

func (f *fakeScraper) Scrape(ctx context.Context, url string, opts *scraper.ScrapeOptions) (*scraper.Document, error) {
	f.url = url
	return f.doc, f.err
}

func TestNew_RequiresProvider(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error when scraper is nil")
	}
}

func TestExecute_FetchesAndTruncates(t *testing.T) {
	long := strings.Repeat("x", 1000)
	c, err := New(&fakeScraper{doc: &scraper.Document{Text: long}}, WithMaxChars(50))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"url": "https://example.com/page",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	r, ok := got.(Result)
	if !ok {
		t.Fatalf("got = %T", got)
	}
	if r.URL != "https://example.com/page" {
		t.Errorf("URL = %q", r.URL)
	}
	if len(r.Text) != 50 {
		t.Errorf("text length = %d, want 50", len(r.Text))
	}
	if r.RetrievedAt.IsZero() {
		t.Error("RetrievedAt was zero")
	}
}

func TestExecute_RejectsInvalidURL(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}})
	cases := []map[string]any{
		{},
		{"url": ""},
		{"url": "not a url"},
		{"url": "ftp://example.com"},
		{"url": "https://"},
	}
	for _, params := range cases {
		if _, err := c.Execute(context.Background(), ToolName, params); err == nil {
			t.Errorf("expected error for params %v", params)
		}
	}
}

func TestExecute_DomainFilterAllow(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}},
		WithAllowedDomains("go.dev"))

	_, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://blog.example/x"})
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("expected ErrURLNotAllowed, got %v", err)
	}

	_, err = c.Execute(context.Background(), ToolName, map[string]any{"url": "https://go.dev/x"})
	if err != nil {
		t.Errorf("go.dev should be allowed; got %v", err)
	}

	_, err = c.Execute(context.Background(), ToolName, map[string]any{"url": "https://blog.go.dev/x"})
	if err != nil {
		t.Errorf("blog.go.dev should match go.dev; got %v", err)
	}
}

func TestExecute_DomainFilterBlock(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}},
		WithBlockedDomains("medium.com"))

	_, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://medium.com/x"})
	if !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("expected ErrURLNotAllowed; got %v", err)
	}
}

func TestResult_FormatsMarkdown(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}})
	out := c.Result(ToolName, Result{URL: "https://x", Title: "T", Text: "body"})
	text := out.Parts[0].Text
	for _, want := range []string{"https://x", "Title: T", "body"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
}
