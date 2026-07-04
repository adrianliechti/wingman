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
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: long}}, WithMaxChars(50))

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"url": "https://example.com/page",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text, ok := got.(string)
	if !ok {
		t.Fatalf("Execute returned %T, want string", got)
	}
	if !strings.HasPrefix(text, "Source: https://example.com/page\n\n") {
		t.Errorf("missing source header in:\n%s", text)
	}
	if !strings.Contains(text, strings.Repeat("x", 50)+"\n\n[Truncated: showing characters 0-50 of 1000. Fetch again with start_index=50 to continue.]") {
		t.Errorf("missing truncated body with notice in:\n%s", text)
	}
}

func TestExecute_StartIndexContinues(t *testing.T) {
	long := strings.Repeat("a", 50) + strings.Repeat("b", 50)
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: long}}, WithMaxChars(50))

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"url":         "https://example.com/page",
		"start_index": float64(50),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text := got.(string)
	if !strings.Contains(text, strings.Repeat("b", 50)) {
		t.Errorf("missing continuation in:\n%s", text)
	}
	if strings.Contains(text, "Truncated") {
		t.Errorf("unexpected truncation notice in:\n%s", text)
	}
}

func TestExecute_StartIndexBeyondEnd(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "short"}})

	got, err := c.Execute(context.Background(), ToolName, map[string]any{
		"url":         "https://example.com/page",
		"start_index": float64(100),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(got.(string), "beyond the end of the page (5 characters total)") {
		t.Errorf("got:\n%s", got)
	}
}

func TestExecute_RejectsInvalidURL(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}})
	for _, params := range []map[string]any{
		{},
		{"url": ""},
		{"url": "not a url"},
		{"url": "ftp://example.com"},
		{"url": "https://"},
	} {
		if _, err := c.Execute(context.Background(), ToolName, params); err == nil {
			t.Errorf("expected error for %v", params)
		}
	}
}

func TestExecute_DomainFilterAllow(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}}, WithAllowedDomains("go.dev"))

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://blog.example/x"}); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("expected ErrURLNotAllowed; got %v", err)
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://go.dev/x"}); err != nil {
		t.Errorf("go.dev should be allowed; got %v", err)
	}
	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://blog.go.dev/x"}); err != nil {
		t.Errorf("blog.go.dev should match go.dev; got %v", err)
	}
}

func TestExecute_DomainFilterBlock(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}}, WithBlockedDomains("medium.com"))

	if _, err := c.Execute(context.Background(), ToolName, map[string]any{"url": "https://medium.com/x"}); !errors.Is(err, ErrURLNotAllowed) {
		t.Errorf("expected ErrURLNotAllowed; got %v", err)
	}
}

func TestResult_PassesThroughText(t *testing.T) {
	c, _ := New(&fakeScraper{doc: &scraper.Document{Text: "ok"}})
	out := c.Result(ToolName, "some markdown")
	if len(out.Parts) != 1 || out.Parts[0].Text != "some markdown" {
		t.Errorf("got %+v", out)
	}
}
