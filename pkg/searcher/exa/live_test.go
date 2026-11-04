package exa

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/searcher"

	"github.com/joho/godotenv"
)

// Opt-in real retrieval test. Never log request headers or credentials.
func TestSearchLive(t *testing.T) {
	if os.Getenv("EXA_LIVE_TEST") != "1" {
		t.Skip("set EXA_LIVE_TEST=1 and EXA_API_KEY to exercise Exa search")
	}
	// Match the server's opt-in live probes; never print upstream credentials.
	_ = godotenv.Load("../../../.env")
	token := os.Getenv("EXA_API_KEY")
	if token == "" {
		t.Fatal("EXA_API_KEY is required")
	}
	for _, tc := range []struct {
		mode     string
		query    string
		evidence string
	}{
		{"auto", "AgentWebBench number websites documents", "18.4"},
		{"lite", "2021 Cannes Palme d'Or winning film", "titane"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			rawChars := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err != nil {
					return nil, err
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					return nil, err
				}
				var data SearchResponse
				if err := json.Unmarshal(body, &data); err == nil {
					for _, result := range data.Results {
						rawChars += len([]rune(result.Text))
					}
				}
				resp.Body = io.NopCloser(bytes.NewReader(body))
				return resp, nil
			})
			c, err := New(token, WithClient(&http.Client{Transport: transport}), WithMode(tc.mode))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			limit := 3
			start := time.Now()
			results, err := c.Search(ctx, tc.query, &searcher.SearchOptions{Limit: &limit})
			if err != nil {
				t.Fatal(err)
			}
			chars, found := 0, false
			for _, result := range results {
				chars += len([]rune(result.Excerpt))
				found = found || strings.Contains(strings.ToLower(result.Excerpt), tc.evidence)
				if result.Source == "" {
					t.Error("result lacks source")
				}
			}
			if !found {
				t.Error("known answer missing from the retrieved excerpts")
			}
			t.Logf("results=%d full_text_chars=%d excerpt_chars=%d elapsed=%s", len(results), rawChars, chars, time.Since(start).Round(time.Millisecond))
		})
	}
}
