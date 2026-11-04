package exa

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/joho/godotenv"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestScrapePreservesEntireExtraction(t *testing.T) {
	text := strings.Repeat("Early source text. ", 1000) + "Late evidence: VX-7319."
	client, err := New("key", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var request map[string]any
		if err := json.UnmarshalRead(req.Body, &request); err != nil {
			t.Fatal(err)
		}
		if request["text"] != true || request["maxAgeHours"] != float64(0) {
			t.Fatalf("request must extract fresh text: %+v", request)
		}
		response, _ := json.Marshal(ContentsResponse{Results: []ContentsResult{{Text: text}}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(response))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := client.Scrape(t.Context(), "https://source.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text != text {
		t.Fatal("full source text was lost")
	}
}

// Compare Exa extraction upstream of any tool pagination or passage selection.
// Opt in because this makes billable live extraction requests.
func TestLiveExtractionModes(t *testing.T) {
	if os.Getenv("EXA_EXTRACTION_LIVE") != "1" {
		t.Skip("set EXA_EXTRACTION_LIVE=1 for live extraction comparison")
	}
	_ = godotenv.Load("../../../.env")
	key := os.Getenv("EXA_API_KEY")
	if key == "" {
		t.Skip("missing EXA_API_KEY")
	}
	url := os.Getenv("EXA_EXTRACTION_URL")
	if url == "" {
		url = "https://www.blick.ch/"
	}
	type observation struct {
		Mode         string `json:"mode"`
		Characters   int    `json:"characters"`
		Milliseconds int64  `json:"milliseconds"`
		Text         string `json:"text"`
	}
	var results []observation
	for _, mode := range []string{"default", "standard", "full", "full-html", "full-sections"} {
		t.Run(mode, func(t *testing.T) {
			client, err := New(key, WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.UnmarshalRead(req.Body, &body); err != nil {
					return nil, err
				}
				req.Body.Close()
				body["text"] = true
				if mode != "default" {
					body["text"] = map[string]any{"verbosity": mode}
				}
				if mode == "full-html" {
					body["text"] = map[string]any{"verbosity": "full", "includeHtmlTags": true}
				}
				if mode == "full-sections" {
					body["text"] = map[string]any{"verbosity": "full", "includeSections": []string{"header", "navigation", "banner", "body", "sidebar", "footer", "metadata", "unspecified"}}
				}
				data, err := json.Marshal(body)
				if err != nil {
					return nil, err
				}
				req.Body = io.NopCloser(bytes.NewReader(data))
				req.ContentLength = int64(len(data))
				return http.DefaultTransport.RoundTrip(req)
			})}))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			start := time.Now()
			doc, err := client.Scrape(ctx, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			result := observation{mode, utf8.RuneCountInString(doc.Text), time.Since(start).Milliseconds(), doc.Text}
			results = append(results, result)
			t.Logf("%s: %d characters in %dms", mode, result.Characters, result.Milliseconds)
			if strings.TrimSpace(doc.Text) == "" {
				t.Error("empty extraction")
			}
		})
	}
	if path := os.Getenv("EXA_EXTRACTION_OUTPUT"); path != "" {
		data, err := json.Marshal(results, jsontext.WithIndent("  "), json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
