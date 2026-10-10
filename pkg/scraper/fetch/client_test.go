package fetch_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adrianliechti/wingman/pkg/scraper/fetch"

	"github.com/stretchr/testify/require"
)

func TestScrapeHTML(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
  <nav><a href="/">Home</a></nav>
  <main>
    <h1>Hello World</h1>
    <p>This is the main content.</p>
  </main>
  <footer>Copyright 2026</footer>
</body>
</html>`)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Hello World")
	require.Contains(t, result.Text, "main content")
	require.NotContains(t, result.Text, "Copyright 2026")
}

func TestScrapeHTMLFallbackToBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
  <div>
    <h1>Page Title</h1>
    <p>Some body text here.</p>
  </div>
</body>
</html>`)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Page Title")
	require.Contains(t, result.Text, "Some body text here.")
}

func TestScrapeStripsScriptAndStyle(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html>
<head><style>body { color: red; }</style></head>
<body>
  <script>alert("hello")</script>
  <p>Visible text</p>
  <style>.hidden { display: none; }</style>
</body>
</html>`)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Visible text")
	require.NotContains(t, result.Text, "alert")
	require.NotContains(t, result.Text, "color: red")
	require.NotContains(t, result.Text, "display: none")
}

func TestScrapePlainText(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Just plain text content.")
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Equal(t, "Just plain text content.", result.Text)
}

func TestScrapeFollowsRedirects(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})

	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><p>Final destination</p></body></html>`)
	})

	handler := mux

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test/redirect", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Final destination")
}

func TestScrapeErrorStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	_, err = c.Scrape(context.Background(), "https://source.test", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

func TestScrapeAriaHidden(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
  <div aria-hidden="true">Hidden overlay</div>
  <p>Actual content</p>
</body></html>`)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Actual content")
	require.NotContains(t, result.Text, "Hidden overlay")
}

func TestScrapeArticleFallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
  <div class="sidebar">Sidebar junk</div>
  <article>
    <h2>Article Title</h2>
    <p>Article body.</p>
  </article>
</body></html>`)
	})

	c, err := fetch.New(fetch.WithClient(&http.Client{Transport: handlerTransport{handler}}))
	require.NoError(t, err)

	result, err := c.Scrape(context.Background(), "https://source.test", nil)
	require.NoError(t, err)

	require.Contains(t, result.Text, "Article Title")
	require.Contains(t, result.Text, "Article body.")
	require.NotContains(t, result.Text, "Sidebar junk")
}

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w.Result(), nil
}
